package sign

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"net/url"
	"strings"
	"testing"
)

func TestVideoTabSignatureUsesOrderedGETQueryAndEmptyBodyDigest(t *testing.T) {
	ent := Entropy{MessageRandom: 123, Random: 456, PackedRandom: 789, HeliosRandom: 1000}
	pairs := [][2]string{{"z", "空 格+"}, {"a", "1"}, {"_rticket", "old"}}
	query, headers, err := signVideoTab(pairs, 1794820000123, "7100000000000001", ent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(query, "z=%E7%A9%BA%20%E6%A0%BC%2B&a=1&_rticket=") || strings.Count(query, "_rticket=") != 1 {
		t.Fatal("GET query order or wire escaping changed")
	}
	values, _ := url.ParseQuery(query)
	if values.Get("z") != "空 格+" {
		t.Fatal("escaped query does not round trip")
	}
	digest, body := sm3Sum([]byte(query)), md5.Sum(nil)
	if v5Branch(digest[:], body[:], 1794820000) != 12 {
		t.Fatal("GET selected a branch using the wrong body digest")
	}
	if len(headers) != 3 || headers["x-khronos"] != "1794820000" || headers["x-helios"] == "" {
		t.Fatal("incorrect GET signature header set")
	}
	encoded, err := base64.StdEncoding.DecodeString(headers["x-medusa"])
	if err != nil || len(encoded) < 128 || binary.LittleEndian.Uint32(encoded[:4])^1794820000 != 5 {
		t.Fatal("missing V5 GET envelope")
	}
	_, changed, err := signVideoTab([][2]string{{"z", "空 格+"}, {"a", "2"}}, 1794820000123, "7100000000000001", ent)
	if err != nil || changed["x-medusa"] == headers["x-medusa"] {
		t.Fatal("GET signature did not bind the transmitted query")
	}
	_, differentDevice, err := signVideoTab(pairs, 1794820000123, "7100000000000002", ent)
	if err != nil || differentDevice["x-medusa"] == headers["x-medusa"] {
		t.Fatal("GET signature did not bind the device")
	}
}

func TestVideoTabProcessContextSurvivesSubsequentRequests(t *testing.T) {
	first := tabProcess(1794820000123)
	second := tabProcess(1794820010123)
	if first != second || first.launched <= 0 {
		t.Fatal("GET process context was regenerated per request")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(first.id)
	if err != nil || len(decoded) != 18 {
		t.Fatal("GET process identifier does not match the reference format")
	}
}
