package store

import (
	"database/sql"
	"time"
)

// saveEpisodeProgressTx preserves a per-episode clock alongside the compact
// one-row-per-series history summary. Watched status is monotonic locally;
// rewinding or replaying an episode must not make it appear unwatched.
func saveEpisodeProgressTx(tx *sql.Tx, item EpisodeProgress, scope string) error {
	if item.SeriesID == "" || item.EpisodeNum <= 0 {
		return nil
	}
	if item.UpdatedAt <= 0 {
		item.UpdatedAt = time.Now().Unix()
	}
	if item.PositionSec <= 0 {
		_, err := tx.Exec(`
			UPDATE episode_progress SET
				episode_id = CASE WHEN ? != '' THEN ? ELSE episode_id END,
				duration_sec = MAX(duration_sec, ?),
				updated_at = MAX(updated_at, ?),
				position_sec = 0
			WHERE scope = ? AND series_id = ? AND episode_num = ? AND updated_at <= ?
		`, item.EpisodeID, item.EpisodeID, item.DurationSec, item.UpdatedAt,
			scope, item.SeriesID, item.EpisodeNum, item.UpdatedAt)
		return err
	}
	_, err := tx.Exec(`
		INSERT INTO episode_progress (
			scope, series_id, episode_num, episode_id, position_sec, duration_sec, updated_at, watched
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(scope, series_id, episode_num) DO UPDATE SET
			episode_id = CASE WHEN excluded.episode_id != '' THEN excluded.episode_id ELSE episode_progress.episode_id END,
			position_sec = CASE WHEN excluded.updated_at >= episode_progress.updated_at THEN excluded.position_sec ELSE episode_progress.position_sec END,
		duration_sec = MAX(episode_progress.duration_sec, excluded.duration_sec),
			updated_at = MAX(episode_progress.updated_at, excluded.updated_at),
			watched = MAX(episode_progress.watched, excluded.watched)
	`, scope, item.SeriesID, item.EpisodeNum, item.EpisodeID, item.PositionSec, item.DurationSec, item.UpdatedAt,
		boolInt(item.Watched || item.PositionSec > 0))
	return err
}

func (s *Store) EpisodeProgress(seriesID string, scopes ...string) ([]EpisodeProgress, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`
		SELECT series_id, episode_num, episode_id, position_sec, duration_sec, updated_at, watched
		FROM episode_progress
		WHERE scope = ? AND series_id = ?
		ORDER BY episode_num
	`, s.syncScopeLocked(scopes), seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []EpisodeProgress
	for rows.Next() {
		var item EpisodeProgress
		var watched int
		if err := rows.Scan(&item.SeriesID, &item.EpisodeNum, &item.EpisodeID, &item.PositionSec, &item.DurationSec, &item.UpdatedAt, &watched); err != nil {
			return nil, err
		}
		// Older databases only set this flag near completion. Their positive
		// playback clocks are already sufficient evidence of a viewed episode.
		item.Watched = watched != 0 || item.PositionSec > 0
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) SaveEpisodeProgressBatchIfCurrentScope(items []EpisodeProgress, scope string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope != s.syncScope {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	for _, item := range items {
		if err := saveEpisodeProgressTx(tx, item, scope); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

// ReplaceEpisodeProgressIfCurrentScope applies a complete cloud snapshot after
// the history summary has been replaced. The cloud may only retain the latest
// episode of a series, so keep earlier local episode clocks for surviving series.
// The caller must read every remote page and let pending local writes settle.
func (s *Store) ReplaceEpisodeProgressIfCurrentScope(items []EpisodeProgress, scope string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if scope != s.syncScope {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM episode_progress WHERE scope = ? AND series_id NOT IN (SELECT series_id FROM history)`, scope); err != nil {
		return false, err
	}
	for _, item := range items {
		if err := saveEpisodeProgressTx(tx, item, scope); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

func (s *Store) MarkEpisodeProgressWatchedIfCurrentScope(seriesID string, episodeNumbers []int, scope string) error {
	if len(episodeNumbers) == 0 {
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
	for _, episodeNum := range episodeNumbers {
		if episodeNum <= 0 {
			continue
		}
		if _, err := tx.Exec(`UPDATE episode_progress SET watched = 1 WHERE scope = ? AND series_id = ? AND episode_num = ? AND position_sec > 0`, scope, seriesID, episodeNum); err != nil {
			return err
		}
	}
	return tx.Commit()
}
