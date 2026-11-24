//go:build windows

package store

import (
	"database/sql"

	_ "modernc.org/sqlite"
	"myguo/internal/sqlitedsn"
)

// legacyProgressRows reads the legacy hgplayer database with the in-process
// SQLite driver. Windows has no /usr/bin/sqlite3; the startup caller is already
// gated to darwin, so this path is only reachable from direct callers such as
// migration tests. The returned rows match the sqlite3 -json shape consumed by
// importOldRecords (numeric values as float64).
func legacyProgressRows(dbPath string) ([]map[string]any, error) {
	db, err := sql.Open("sqlite", sqlitedsn.FileURL(dbPath).String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(legacyProgressQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []map[string]any
	for rows.Next() {
		var seriesID, series sql.NullString
		var episodeIndex, updatedAt sql.NullInt64
		var positionSec sql.NullFloat64
		if err := rows.Scan(&seriesID, &episodeIndex, &positionSec, &updatedAt, &series); err != nil {
			return nil, err
		}
		result = append(result, map[string]any{
			"series_id":     seriesID.String,
			"episode_index": float64(episodeIndex.Int64),
			"position_sec":  positionSec.Float64,
			"updated_at":    float64(updatedAt.Int64),
			"series":        series.String,
		})
	}
	return result, rows.Err()
}
