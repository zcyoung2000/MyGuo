package media

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"myguo/internal/hgapi/model"
	"myguo/internal/mediautil"
)

func normalizeVideoQuality(quality string) string {
	return mediautil.NormalizeVideoQuality(quality)
}

func FFmpegCommand() (string, error) { return ffmpegCommand() }

func episodeCachePath(cacheDir, episodeID, quality string) string {
	return EpisodeCachePath(cacheDir, episodeID, quality)
}

func qualityLabel(quality string) string {
	return strings.ToUpper(quality)
}

func EpisodeCachePath(cacheDir, episodeID, quality string) string {
	return episodeCachePathForPlatform(cacheDir, episodeID, quality, runtime.GOOS == "windows")
}

func episodeCachePathForPlatform(cacheDir, episodeID, quality string, windows bool) string {
	if !validContentID(episodeID) {
		return ""
	}
	name := episodeID
	if quality != DefaultVideoQuality {
		name += "_" + quality
	}
	name += playbackCacheSuffix(windows)
	name += "_v2"
	return filepath.Join(cacheDir, "episodes", name+".mp4")
}

func playbackCacheSuffix(windows bool) string {
	if windows {
		// Older Windows caches may contain HEVC files that this build's
		// Media Foundation player cannot open. Keep the compatibility-selected
		// rendition in a separate namespace so playback re-resolves its source.
		return "_windows_compat"
	}
	return ""
}

func usesWindowsH264Preference() bool { return runtime.GOOS == "windows" }

func definitionNumber(definition string) int {
	number := 0
	for _, char := range definition {
		if char < '0' || char > '9' {
			break
		}
		number = number*10 + int(char-'0')
	}
	return number
}

func streamQualityRank(stream model.Stream) int {
	if number := definitionNumber(stream.Definition); number > 0 {
		return number
	}
	if stream.Height > 0 {
		return stream.Height
	}
	return stream.Width
}

func nearestQuality(requested string, candidates []string) string {
	if len(candidates) == 0 {
		return normalizeVideoQuality(requested)
	}

	requestedRank := definitionNumber(normalizeVideoQuality(requested))
	selected := candidates[0]
	selectedGap := abs(definitionNumber(selected) - requestedRank)
	for _, candidate := range candidates[1:] {
		gap := abs(definitionNumber(candidate) - requestedRank)
		if gap < selectedGap || (gap == selectedGap && definitionNumber(candidate) > definitionNumber(selected)) {
			selected = candidate
			selectedGap = gap
		}
	}
	return selected
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func qualitySelectionFromPlayback(pb *model.Playback, requested string) QualitySelection {
	return qualitySelectionFromPlaybackWithH264Preference(pb, requested, usesWindowsH264Preference())
}

func qualitySelectionFromPlaybackWithH264Preference(pb *model.Playback, requested string, preferH264 bool) QualitySelection {
	requested = normalizeVideoQuality(requested)
	available := make(map[string]bool)
	streamDetails := make(map[string]model.Stream)
	if pb != nil {
		for _, stream := range preferredVideoStreams(pb.Streams, preferH264) {
			if strings.TrimSpace(stream.URL) == "" {
				continue
			}
			rank := streamQualityRank(stream)
			if rank <= 0 {
				continue
			}
			mappedQuality := qualityForStreamRank(rank)
			available[mappedQuality] = true
			if _, exists := streamDetails[mappedQuality]; !exists || stream.Bitrate > streamDetails[mappedQuality].Bitrate {
				streamDetails[mappedQuality] = stream
			}
		}
	}

	candidates := make([]string, 0, len(available))
	for _, quality := range videoQualities {
		if available[quality] {
			candidates = append(candidates, quality)
		}
	}
	selected := nearestQuality(requested, candidates)

	options := make([]QualityOptionDTO, 0, len(videoQualities))
	for _, quality := range videoQualities {
		option := QualityOptionDTO{
			ID:        quality,
			Label:     qualityLabel(quality),
			Available: available[quality],
			Active:    quality == selected,
		}
		if stream, ok := streamDetails[quality]; ok {
			option.Codec = stream.Codec
			option.Width = stream.Width
			option.Height = stream.Height
			option.Bitrate = stream.Bitrate
		}
		options = append(options, option)
	}
	durationSec := 0
	if pb != nil && pb.DurationSeconds > 0 {
		durationSec = pb.DurationSeconds
	}
	return QualitySelection{Options: options, Selected: selected, DurationSec: durationSec}
}

func qualityForStreamRank(rank int) string {
	selected := DefaultVideoQuality
	selectedGap := abs(rank - definitionNumber(selected))
	for _, quality := range videoQualities {
		gap := abs(rank - definitionNumber(quality))
		if gap < selectedGap || (gap == selectedGap && definitionNumber(quality) > definitionNumber(selected)) {
			selected = quality
			selectedGap = gap
		}
	}
	return selected
}

func selectVideoStream(pb *model.Playback, requested string) (model.Stream, error) {
	return selectVideoStreamWithH264Preference(pb, requested, usesWindowsH264Preference())
}

func selectVideoStreamWithH264Preference(pb *model.Playback, requested string, preferH264 bool) (model.Stream, error) {
	if pb == nil {
		return model.Stream{}, fmt.Errorf("playback is unavailable")
	}
	quality := normalizeVideoQuality(requested)
	streams := preferredVideoStreams(pb.Streams, preferH264)
	preferred := *pb
	preferred.Streams = streams
	stream, err := preferred.Select(quality)
	if err == nil {
		return stream, nil
	}

	type rankedStream struct {
		rank   int
		stream model.Stream
	}
	candidates := make([]rankedStream, 0, len(streams))
	for _, candidate := range streams {
		if strings.TrimSpace(candidate.URL) == "" {
			continue
		}
		candidates = append(candidates, rankedStream{
			rank:   streamQualityRank(candidate),
			stream: candidate,
		})
	}
	if len(candidates) == 0 {
		return model.Stream{}, err
	}

	requestedRank := definitionNumber(quality)
	selected := candidates[0]
	selectedGap := abs(selected.rank - requestedRank)
	for _, candidate := range candidates[1:] {
		gap := abs(candidate.rank - requestedRank)
		if gap < selectedGap || (gap == selectedGap && candidate.rank > selected.rank) {
			selected = candidate
			selectedGap = gap
		}
	}
	return selected.stream, nil
}

func preferredVideoStreams(streams []model.Stream, preferH264 bool) []model.Stream {
	if !preferH264 {
		return streams
	}
	h264 := make([]model.Stream, 0, len(streams))
	for _, stream := range streams {
		if strings.TrimSpace(stream.URL) != "" && isH264Codec(stream.Codec) {
			h264 = append(h264, stream)
		}
	}
	if len(h264) > 0 {
		return h264
	}
	return streams
}

func isH264Codec(codec string) bool {
	codec = strings.ToLower(strings.TrimSpace(codec))
	return codec == "h264" || codec == "avc" || codec == "avc1" || strings.HasPrefix(codec, "avc1.")
}
