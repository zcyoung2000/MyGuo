package playback

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
)

type DanmakuPreferences struct {
	Enabled    bool    `json:"enabled"`
	Opacity    float64 `json:"opacity"`
	FontSize   float64 `json:"fontSize"`
	FontWeight int     `json:"fontWeight"`
	Outline    bool    `json:"outline"`
	Rows       int     `json:"rows"`
}
type Preferences struct {
	Version int                `json:"version"`
	Quality string             `json:"quality"`
	Rate    float64            `json:"rate"`
	Volume  float64            `json:"volume"`
	Muted   bool               `json:"muted"`
	Fit     string             `json:"fit"`
	Clear   bool               `json:"-"` // Current overlay visibility belongs to the playback session.
	Danmaku DanmakuPreferences `json:"danmaku"`
}

func DefaultPreferences() Preferences {
	return Preferences{Version: 1, Quality: "1080p", Rate: 1, Volume: 1, Fit: "contain", Danmaku: DanmakuPreferences{Enabled: true, Opacity: 1, FontSize: 18, FontWeight: 600, Outline: true}}
}
func finiteClamp(v, min, max, fallback float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fallback
	}
	return math.Max(min, math.Min(max, v))
}
func (p Preferences) Normalized() Preferences {
	switch p.Quality {
	case "360p", "480p", "540p", "720p", "1080p", "2k", "4k":
	default:
		p.Quality = "1080p"
	}
	p.Version = 1
	p.Rate = finiteClamp(p.Rate, 0.25, 2, 1)
	p.Volume = finiteClamp(p.Volume, 0, 1, 1)
	if p.Fit != "cover" {
		p.Fit = "contain"
	}
	d := &p.Danmaku
	d.Opacity = finiteClamp(d.Opacity, 0.2, 1, 1)
	d.FontSize = finiteClamp(d.FontSize, 14, 40, 18)
	d.FontWeight = max(400, min(900, d.FontWeight))
	d.Rows = max(0, min(15, d.Rows))
	return p
}
func LoadPreferences(path string) Preferences {
	p := DefaultPreferences()
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &p) != nil {
		return DefaultPreferences()
	}
	return p.Normalized()
}
func SavePreferences(path string, p Preferences) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(p.Normalized(), "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".player-prefs-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func (c *Controller) persistPreferences() {
	// Serialized by the actor, so an older write never replaces a newer one.
	if err := SavePreferences(c.preferencesPath, c.state.Preferences); err != nil {
		c.state.Notice = "播放设置保存失败，请检查磁盘空间"
	}
}
