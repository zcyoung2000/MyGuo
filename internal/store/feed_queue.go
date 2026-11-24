package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

const recentFeedLimit = 200

// Use SQLite insertion order so the window stays deterministic even when
// timestamps tie or the system clock changes. Refreshing an ID moves it last.
func saveFeedSeenTx(ctx context.Context, tx *sql.Tx, channel string, generation int64, ids []string) error {
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM feed_seen WHERE channel = ? AND series_id = ?`, channel, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO feed_seen (channel, generation, series_id, seen_at) VALUES (?, ?, ?, ?)`, channel, generation, id, time.Now().Unix()); err != nil {
			return err
		}
	}
	// Also collapse duplicate IDs left by the old generation-based schema.
	_, err := tx.ExecContext(ctx, `DELETE FROM feed_seen WHERE channel = ? AND rowid NOT IN (
		SELECT MAX(rowid) FROM feed_seen WHERE channel = ?
		GROUP BY series_id ORDER BY MAX(rowid) DESC LIMIT ?
	)`, channel, channel, recentFeedLimit)
	return err
}

// FeedQueueEntry contains catalog metadata, never process-local media URLs.
// Fetching an entry is not a view: it remains pending until its first frame.
type FeedQueueEntry struct {
	SeriesID string
	Payload  json.RawMessage
}

func (s *Store) AppendFeedQueue(channel string, entries []FeedQueueEntry) error {
	if len(entries) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := appendFeedQueueTx(context.Background(), tx, channel, entries); err != nil {
		return err
	}
	return tx.Commit()
}

func appendFeedQueueTx(ctx context.Context, tx *sql.Tx, channel string, entries []FeedQueueEntry) error {
	for _, entry := range entries {
		if entry.SeriesID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO feed_queue (channel, series_id, payload) VALUES (?, ?, ?)
			ON CONFLICT(channel, series_id) DO NOTHING
		`, channel, entry.SeriesID, []byte(entry.Payload)); err != nil {
			return err
		}
	}
	return nil
}

// Persist a fetched page before publishing it. Cursor, recent IDs and pending
// metadata must either all commit or all remain at the previous page.
func (s *Store) SaveRecommendPage(ctx context.Context, channel, cursor string, generation int64, entries []FeedQueueEntry, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := appendFeedQueueTx(ctx, tx, channel, entries); err != nil {
		return err
	}
	if err := saveFeedSeenTx(ctx, tx, channel, generation, ids); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO feed_progress (channel, cursor, generation, updated_at)
		VALUES (?, ?, ?, ?) ON CONFLICT(channel) DO UPDATE SET
		cursor = excluded.cursor, generation = excluded.generation, updated_at = excluded.updated_at`, channel, cursor, generation, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetFeedQueue(channel string) ([]FeedQueueEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT series_id, payload FROM feed_queue WHERE channel = ? ORDER BY position`, channel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]FeedQueueEntry, 0)
	for rows.Next() {
		var entry FeedQueueEntry
		if err := rows.Scan(&entry.SeriesID, &entry.Payload); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// RemoveFeedQueueItem rejects one catalog entry without acknowledging the
// earlier, still-unviewed recommendations as consumed.
func (s *Store) RemoveFeedQueueItem(channel, seriesID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM feed_queue WHERE channel = ? AND series_id = ?`, channel, seriesID)
	return err
}

// RemoveUnavailableFeedSeries rejects a confirmed unplayable work in every
// channel, without consuming any adjacent recommendations or watching history.
func (s *Store) RemoveUnavailableFeedSeries(seriesID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM feed_queue WHERE series_id = ?`, seriesID)
	return err
}

func (s *Store) ConsumeFeedQueue(channel, seriesID string) error {
	return s.ConsumeFeedQueueContext(context.Background(), channel, seriesID)
}

func (s *Store) ConsumeFeedQueueContext(ctx context.Context, channel, seriesID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT series_id FROM feed_queue WHERE channel = ? AND position <= (
		SELECT position FROM feed_queue WHERE channel = ? AND series_id = ?
	) ORDER BY position`, channel, channel, seriesID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil // A late acknowledgement cannot consume a newer queue tail.
	}
	generation := int64(1)
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM feed_progress WHERE channel = ?`, channel).Scan(&generation); err != nil && err != sql.ErrNoRows {
		return err
	}
	if err := saveFeedSeenTx(ctx, tx, channel, generation, ids); err != nil {
		return err
	}
	// A forward swipe also skips earlier pending entries. A late acknowledgement
	// for an already removed entry cannot delete any newer recommendations.
	_, err = tx.ExecContext(ctx, `DELETE FROM feed_queue WHERE channel = ? AND position <= (
		SELECT position FROM feed_queue WHERE channel = ? AND series_id = ?
	)`, channel, channel, seriesID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
