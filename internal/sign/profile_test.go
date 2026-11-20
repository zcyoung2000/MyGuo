package sign

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"net/url"
	"testing"
)

func TestV5EnvelopeAndF13BranchBindWireBytes(t *testing.T) {
	body := []byte(`{"mixed_video_id_map":{"1004":["7100000000000201"]}}`)
	ent := Entropy{MessageRandom: 123, Random: 456, PackedRandom: 789, HeliosRandom: 1000, GorgonRandom: 2000, EnvLaunch: 3, EnvPID: 4}
	q, h, err := SignProfile(PlaybackProfile, Full, "https://api5-normal-lq.fqnovel.com", "/novel/player/multi_video_model/player/v1", [][2]string{{"aid", "8662"}, {"iid", "7100000000000002"}}, 1794820000123, "7100000000000001", body, &ent)
	if err != nil {
		t.Fatal(err)
	}
	ts := uint32(1794820000)
	sum := md5.Sum(body)
	queryHash := sm3Sum([]byte(q))
	if v5Branch(queryHash[:], sum[:], ts) != 12 {
		t.Fatal("selected unsupported V5 branch")
	}
	encoded, err := base64.StdEncoding.DecodeString(h["x-medusa"])
	if err != nil {
		t.Fatal(err)
	}
	var prefix [4]byte
	copy(prefix[:], encoded[:4])
	timestamp := le32(ts)
	for i := range prefix {
		prefix[i] ^= timestamp[i]
	}
	if binary.LittleEndian.Uint32(prefix[:]) != 5 {
		t.Fatal("Medusa envelope is not version 5")
	}
	values, _ := url.ParseQuery(q)
	if values.Get("ts") != "1794820000" {
		t.Fatal("signature clock mismatch")
	}
	other, h2, err := SignProfile(PlaybackProfile, Full, "https://api5-normal-lq.fqnovel.com", "/novel/player/multi_video_model/player/v1", [][2]string{{"aid", "8662"}, {"iid", "7100000000000002"}}, 1794820000123, "7100000000000001", append(body, ' '), &ent)
	if err != nil || (h2["x-medusa"] == h["x-medusa"] && q == other) {
		t.Fatal("body mutation did not affect signature")
	}
}
