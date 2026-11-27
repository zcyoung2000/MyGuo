package store

import (
	"context"
	"time"
)

type FeedProgress struct {
	Channel    string
	Cursor     string
	Generation int64
}

func (s *Store) GetFeedProgress(channel string) (FeedProgress, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var progress FeedProgress
	err := s.db.QueryRow(`
		SELECT channel, cursor, generation
		FROM feed_progress
		WHERE channel = ?
	`, channel).Scan(&progress.Channel, &progress.Cursor, &progress.Generation)
	if err != nil {
		return FeedProgress{Channel: channel, Generation: 1}, false
	}
	return progress, true
}

func (s *Store) SaveFeedProgress(channel, cursor string, generation int64) error {
	if generation <= 0 {
		generation = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`
		INSERT INTO feed_progress (channel, cursor, generation, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(channel) DO UPDATE SET
			cursor = excluded.cursor,
			generation = excluded.generation,
			updated_at = excluded.updated_at
	`, channel, cursor, generation, time.Now().Unix())
	return err
}

func (s *Store) GetFeedSeenIDs(channel string, generation int64) map[string]bool {
	seen := make(map[string]bool)
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT series_id
		FROM feed_seen
		WHERE channel = ? AND generation = ?
	`, channel, generation)
	if err != nil {
		return seen
	}
	defer rows.Close()

	for rows.Next() {
		var seriesID string
		if err := rows.Scan(&seriesID); err == nil {
			seen[seriesID] = true
		}
	}
	return seen
}

// Recent recommendations survive cursor/session rotation. Pending items are
// also excluded from new pages even when they fall outside the recent window.
func (s *Store) GetRecentFeedSeenIDs(channel string) (map[string]bool, error) {
	seen := make(map[string]bool)
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT series_id FROM (
			SELECT series_id FROM feed_seen WHERE channel = ?
			GROUP BY series_id ORDER BY MAX(rowid) DESC LIMIT ?
		)
		UNION SELECT series_id FROM feed_queue WHERE channel = ?
	`, channel, recentFeedLimit, channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var seriesID string
		if err := rows.Scan(&seriesID); err != nil {
			return nil, err
		}
		seen[seriesID] = true
	}
	return seen, rows.Err()
}

func (s *Store) SaveFeedSeenIDs(channel string, generation int64, seriesIDs []string) error {
	if len(seriesIDs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := saveFeedSeenTx(context.Background(), tx, channel, generation, seriesIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) StartNextFeedGeneration(channel string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var generation int64
	err := s.db.QueryRow(`
		SELECT generation FROM feed_progress WHERE channel = ?
	`, channel).Scan(&generation)
	if err != nil {
		generation = 1
	}
	generation++
	_, err = s.db.Exec(`
		INSERT INTO feed_progress (channel, cursor, generation, updated_at)
		VALUES (?, '', ?, ?)
		ON CONFLICT(channel) DO UPDATE SET
			cursor = '',
			generation = excluded.generation,
			updated_at = excluded.updated_at
	`, channel, generation, time.Now().Unix())
	if err != nil {
		return generation, err
	}
	return generation, nil
}
