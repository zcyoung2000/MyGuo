package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// migrateVerifiedAccountQueues is only called for aliases verified by the
// current official account response. Empty/unowned queues are never adopted.
// It runs in the same transaction that publishes the canonical account.
func migrateVerifiedAccountQueues(tx *sql.Tx, scope string, aliases []string) error {
	for _, alias := range aliases {
		if alias == scope || !strings.HasPrefix(alias, "user:") || len(alias) <= len("user:") {
			continue
		}
		for _, queue := range syncQueues {
			fields := strings.Split(queue.columns, ", ")
			updates := make([]string, 0, len(fields)+1)
			for _, field := range append(fields, "revision") {
				updates = append(updates, field+"=excluded."+field)
			}
			query := fmt.Sprintf(`INSERT INTO %s (%s, scope, revision)
				SELECT %s, ?, revision FROM %s WHERE scope = ?
				ON CONFLICT(%s) DO UPDATE SET %s
				WHERE excluded.revision > %s.revision OR
				(excluded.revision = %s.revision AND excluded.%s > %s.%s)`,
				queue.name, queue.columns, queue.columns, queue.name, queue.key, strings.Join(updates, ","),
				queue.name, queue.name, queue.order, queue.name, queue.order)
			if _, err := tx.Exec(query, scope, alias); err != nil {
				return err
			}
			if _, err := tx.Exec("DELETE FROM "+queue.name+" WHERE scope = ?", alias); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO episode_progress (
			scope, series_id, episode_num, episode_id, position_sec, duration_sec, updated_at, watched
		)
		SELECT ?, series_id, episode_num, episode_id, position_sec, duration_sec, updated_at, watched
		FROM episode_progress WHERE scope = ?
		ON CONFLICT(scope, series_id, episode_num) DO UPDATE SET
			episode_id = CASE WHEN excluded.episode_id != '' THEN excluded.episode_id ELSE episode_progress.episode_id END,
			position_sec = CASE WHEN excluded.updated_at >= episode_progress.updated_at THEN excluded.position_sec ELSE episode_progress.position_sec END,
			duration_sec = MAX(episode_progress.duration_sec, excluded.duration_sec),
			updated_at = MAX(episode_progress.updated_at, excluded.updated_at),
			watched = MAX(episode_progress.watched, excluded.watched)`, scope, alias); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM episode_progress WHERE scope = ?`, alias); err != nil {
			return err
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	// Earlier versions could clear visible rows while retaining these intents.
	// Restore only missing rows; never undo a newer local position or deletion.
	_, err := tx.Exec(`INSERT OR IGNORE INTO history
		(series_id,title,cover_url,episode_num,episode_title,position_sec,duration_sec,total_episodes,updated_at,intro,content_type)
		SELECT p.series_id,p.title,p.cover_url,p.episode_num,p.episode_title,p.position_sec,p.duration_sec,p.total_episodes,p.updated_at,p.intro,p.content_type
		FROM pending_history_uploads p WHERE p.scope = ?
		AND p.updated_at > COALESCE((SELECT cleared_at FROM history_clear_state WHERE id=1),0)
		AND NOT EXISTS(SELECT 1 FROM history_tombstones t WHERE t.series_id=p.series_id)
		AND NOT EXISTS(SELECT 1 FROM pending_history_deletes d WHERE d.scope=p.scope AND d.series_id=p.series_id AND d.deleted_at>=p.updated_at)`, scope)
	return err
}
