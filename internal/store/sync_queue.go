package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// These definitions also migrate queues from versions with a series-only key.
// Unowned legacy intents must never inherit a new login. They are retired by
// CleanupLegacyHistoryMutations after preserving any visible local progress.
var syncQueues = []struct{ name, columns, fields, order string }{
	{"pending_history_uploads", "series_id, episode_id, title, cover_url, episode_num, episode_title, position_sec, duration_sec, total_episodes, updated_at, intro, content_type", `
		series_id TEXT NOT NULL, episode_id TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '', cover_url TEXT NOT NULL DEFAULT '',
		episode_num INTEGER NOT NULL DEFAULT 1, episode_title TEXT NOT NULL DEFAULT '',
		position_sec REAL NOT NULL DEFAULT 0, duration_sec REAL NOT NULL DEFAULT 0,
		total_episodes INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL DEFAULT 0,
		intro TEXT NOT NULL DEFAULT '', content_type INTEGER NOT NULL DEFAULT 1004`, "updated_at"},
	{"pending_history_deletes", "series_id, episode_id, episode_num, deleted_at", `
		series_id TEXT NOT NULL, episode_id TEXT NOT NULL DEFAULT '',
		episode_num INTEGER NOT NULL DEFAULT 1, deleted_at INTEGER NOT NULL DEFAULT 0`, "deleted_at"},
	{"pending_favorite_ops", "series_id, added, updated_at", `
		series_id TEXT NOT NULL, added INTEGER NOT NULL DEFAULT 1,
		updated_at INTEGER NOT NULL DEFAULT 0`, "updated_at"},
}

func (s *Store) initSyncQueues() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, queue := range syncQueues {
		var columns string
		if err := tx.QueryRow(`SELECT group_concat(name, ',') FROM pragma_table_info(?)`, queue.name).Scan(&columns); err != nil {
			return err
		}
		if strings.Contains(","+columns+",", ",scope,") {
			continue
		}
		if _, err := tx.Exec("ALTER TABLE " + queue.name + " RENAME TO " + queue.name + "_unowned"); err != nil {
			return err
		}
		if _, err := tx.Exec("CREATE TABLE " + queue.name + " (" + queue.fields + `,
			scope TEXT NOT NULL DEFAULT '', revision INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(scope, series_id))`); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO " + queue.name + " (" + queue.columns + ") SELECT " + queue.columns + " FROM " + queue.name + "_unowned"); err != nil {
			return err
		}
		if _, err := tx.Exec("DROP TABLE " + queue.name + "_unowned"); err != nil {
			return err
		}
		if _, err := tx.Exec("CREATE INDEX idx_" + queue.name + "_" + strings.TrimSuffix(queue.order, "_at") + " ON " + queue.name + "(scope, " + queue.order + ")"); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS sync_revision_sequence (
		id INTEGER PRIMARY KEY CHECK (id = 1), revision INTEGER NOT NULL DEFAULT 0
	); INSERT OR IGNORE INTO sync_revision_sequence (id, revision) VALUES (1, 0)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS local_history_retention (
		series_id TEXT PRIMARY KEY
	)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS pending_history_metadata (
		scope TEXT NOT NULL,
		series_id TEXT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,
		next_attempt_at INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(scope, series_id)
	);
	CREATE INDEX IF NOT EXISTS idx_pending_history_metadata_retry
		ON pending_history_metadata(scope, next_attempt_at, series_id)`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS unavailable_history_metadata (
		scope TEXT NOT NULL,
		series_id TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '',
		retry_after INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(scope, series_id)
	);
	CREATE INDEX IF NOT EXISTS idx_unavailable_history_metadata_retry
		ON unavailable_history_metadata(scope, retry_after, series_id)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) QueueHistoryMetadata(seriesIDs []string, scope string) error {
	if len(seriesIDs) == 0 || scope == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope != s.syncScope {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for _, seriesID := range seriesIDs {
		if seriesID == "" {
			continue
		}
		var retryAfter sql.NullInt64
		if err := tx.QueryRow(`SELECT retry_after FROM unavailable_history_metadata WHERE scope = ? AND series_id = ?`, scope, seriesID).Scan(&retryAfter); err != nil && err != sql.ErrNoRows {
			return err
		}
		if retryAfter.Valid && retryAfter.Int64 > now {
			continue
		}
		if retryAfter.Valid {
			if _, err := tx.Exec(`DELETE FROM unavailable_history_metadata WHERE scope = ? AND series_id = ?`, scope, seriesID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO pending_history_metadata (scope, series_id) VALUES (?, ?)`, scope, seriesID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) MarkHistoryMetadataUnavailable(item PendingHistoryMetadata, reason string, retryAfter time.Time) error {
	if item.Scope == "" || item.SeriesID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if item.Scope != s.syncScope {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM pending_history_metadata WHERE scope = ? AND series_id = ?`, item.Scope, item.SeriesID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO unavailable_history_metadata (scope, series_id, reason, retry_after)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(scope, series_id) DO UPDATE SET reason = excluded.reason, retry_after = excluded.retry_after`,
		item.Scope, item.SeriesID, reason, retryAfter.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) UnavailableHistoryMetadataCount(scope string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM unavailable_history_metadata WHERE scope = ?`, scope).Scan(&count)
	return count, err
}

func (s *Store) PendingHistoryMetadata(limit int, scope string, now time.Time) ([]PendingHistoryMetadata, error) {
	if limit <= 0 {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`
		SELECT series_id, scope, attempts, next_attempt_at
		FROM pending_history_metadata
		WHERE scope = ? AND next_attempt_at <= ?
		ORDER BY next_attempt_at, series_id
		LIMIT ?`, scope, now.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pending []PendingHistoryMetadata
	for rows.Next() {
		var item PendingHistoryMetadata
		if err := rows.Scan(&item.SeriesID, &item.Scope, &item.Attempts, &item.NextAttemptAt); err != nil {
			return nil, err
		}
		pending = append(pending, item)
	}
	return pending, rows.Err()
}

func (s *Store) RetryHistoryMetadata(items []PendingHistoryMetadata, nextAttempt time.Time) error {
	if len(items) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range items {
		if _, err := tx.Exec(`UPDATE pending_history_metadata SET attempts = attempts + 1, next_attempt_at = ? WHERE scope = ? AND series_id = ?`, nextAttempt.Unix(), item.Scope, item.SeriesID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AckHistoryMetadata(items []PendingHistoryMetadata) error {
	if len(items) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range items {
		if _, err := tx.Exec(`DELETE FROM pending_history_metadata WHERE scope = ? AND series_id = ?`, item.Scope, item.SeriesID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) PendingHistoryMetadataCount(scope string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM pending_history_metadata WHERE scope = ?`, scope).Scan(&count)
	return count, err
}

func (s *Store) NextHistoryMetadataAttempt(scope string) (time.Time, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var next sql.NullInt64
	err := s.db.QueryRow(`SELECT MIN(next_attempt_at) FROM pending_history_metadata WHERE scope = ?`, scope).Scan(&next)
	if err != nil {
		return time.Time{}, false, err
	}
	if !next.Valid {
		return time.Time{}, false, nil
	}
	if next.Int64 <= time.Now().Unix() {
		return time.Now(), true, nil
	}
	return time.Unix(next.Int64, 0), true, nil
}

func (s *Store) SetSyncScope(scope string) {
	s.mu.Lock()
	s.syncScope = scope
	s.mu.Unlock()
}

// PendingHistoryMutationIDs includes intents waiting for retry as well as
// immediately runnable ones. Neither may be removed by a remote snapshot.
func (s *Store) PendingHistoryMutationIDs(scope string) (map[string]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`
		SELECT series_id FROM pending_history_uploads WHERE scope IN (?, '')
		UNION SELECT series_id FROM pending_history_deletes WHERE scope IN (?, '')
	`, scope, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

func (s *Store) PendingOwnedHistoryMutationIDs(scope string) (map[string]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`
		SELECT series_id FROM pending_history_uploads WHERE scope = ?
		UNION SELECT series_id FROM pending_history_deletes WHERE scope = ?
	`, scope, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

// CleanupLegacyHistoryMutations discards tasks whose account ownership was
// never recorded. They cannot safely be sent or retried. Visible history is
// retained locally without keeping an upload task or assigning it to an account.
// A subsequent local playback save establishes ownership through its normal
// scoped upload. Both retirement and retention commit atomically.
func (s *Store) CleanupLegacyHistoryMutations() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT OR IGNORE INTO local_history_retention (series_id)
		SELECT h.series_id FROM history h
		JOIN pending_history_uploads p ON p.series_id = h.series_id
		WHERE p.scope = '' AND length(h.series_id) BETWEEN 6 AND 30
		AND h.series_id NOT GLOB '*[^0-9]*'`); err != nil {
		return 0, err
	}
	var removed int64
	for _, table := range []string{"pending_history_uploads", "pending_history_deletes"} {
		result, err := tx.Exec("DELETE FROM " + table + " WHERE scope = ''")
		if err != nil {
			return 0, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		removed += count
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return removed, nil
}

func (s *Store) PendingUnownedHistoryMutationCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var count int
	err := s.db.QueryRow(`
		SELECT (SELECT COUNT(*) FROM pending_history_uploads WHERE scope = '') +
		       (SELECT COUNT(*) FROM pending_history_deletes WHERE scope = '')
	`).Scan(&count)
	return count, err
}

// Caller holds s.mu. Explicit scopes let an in-flight task keep its original
// identity even if another request changes the service's active credentials.
func (s *Store) syncScopeLocked(scopes []string) string {
	if len(scopes) > 0 {
		return scopes[0]
	}
	return s.syncScope
}

func nextSyncRevision(tx *sql.Tx) (int64, error) {
	var revision int64
	err := tx.QueryRow(`UPDATE sync_revision_sequence SET revision = revision + 1 WHERE id = 1 RETURNING revision`).Scan(&revision)
	return revision, err
}

type syncIntent struct {
	scope, seriesID string
	revision        int64
}

func (s *Store) ackSyncIntents(table string, intents []syncIntent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, intent := range intents {
		if _, err := tx.Exec(fmt.Sprintf(`DELETE FROM %s WHERE scope = ? AND series_id = ? AND revision = ?`, table), intent.scope, intent.seriesID, intent.revision); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AckHistoryUploads(items []PendingHistoryUpload) error {
	intents := make([]syncIntent, 0, len(items))
	for _, item := range items {
		intents = append(intents, syncIntent{item.Scope, item.SeriesID, item.Revision})
	}
	return s.ackSyncIntents("pending_history_uploads", intents)
}

func (s *Store) AckHistoryDeletes(items []PendingHistoryDelete) error {
	intents := make([]syncIntent, 0, len(items))
	for _, item := range items {
		intents = append(intents, syncIntent{item.Scope, item.SeriesID, item.Revision})
	}
	return s.ackSyncIntents("pending_history_deletes", intents)
}

func (s *Store) AckFavoriteOps(items []PendingFavoriteOp) error {
	intents := make([]syncIntent, 0, len(items))
	for _, item := range items {
		intents = append(intents, syncIntent{item.Scope, item.SeriesID, item.Revision})
	}
	return s.ackSyncIntents("pending_favorite_ops", intents)
}
