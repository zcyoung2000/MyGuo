package sign

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type Mode uint8

const (
	Full Mode = iota
	Minimal
	Registration
	Helios
	Passport
	None
)

func (m Mode) String() string {
	if int(m) < 6 {
		return [6]string{"full", "minimal", "registration", "helios", "passport", "none"}[m]
	}
	return "unknown"
}

type AppProfile struct {
	Name                             string
	AID, License                     int64
	Package, VersionName             string
	VersionCode, ManifestVersionCode int
	Channel, AppName, UserAgent      string
	MedusaV5                         bool
}

var PlaybackProfile = AppProfile{
	Name: "hongguo-playback", AID: 8662, License: 646637794, Package: "com.phoenix.read", VersionName: "7.4.3.32",
	VersionCode: 71332, ManifestVersionCode: 71332, Channel: "update_64", AppName: "novelread", MedusaV5: true,
	UserAgent: "com.phoenix.read/71332 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3; Cronet/TTNetVersion:04657795 2026-01-23 QuicVersion:c67e9834 2025-09-08)",
}

// SignProfile binds the signature to the actual URL and transmitted bytes.
// V5 uses a supported F13 branch selected by the ticket; signatures cannot fall
// through to a timestamp-only envelope when a V5 digest cannot be generated.
func SignProfile(profile AppProfile, mode Mode, origin, path string, pairs [][2]string, ticket int64, deviceID string, body []byte, entropy *Entropy) (string, map[string]string, error) {
	if mode > None || ticket < 0 {
		return "", nil, fmt.Errorf("invalid signature profile")
	}
	ent := drawEntropy(ticket)
	if entropy != nil {
		ent = *entropy
	}
	ts := uint32(ticket / 1000)
	if ent.NowSecs == 0 {
		ent.NowSecs = int64(ts)
	}
	values := url.Values{}
	for _, p := range pairs {
		values.Set(p[0], p[1])
	}
	values.Set("ts", strconv.FormatUint(uint64(ts), 10))
	bodyHash := md5Raw(body)
	if body == nil {
		bodyHash = [16]byte{}
	}
	var query string
	for i := int64(0); i < 1024; i++ {
		values.Set("_rticket", strconv.FormatInt(ticket+i, 10))
		query = values.Encode()
		q := sm3Sum([]byte(query))
		if mode == None || (!profile.MedusaV5 && branchOf(q[:], bodyHash[:], le32(ts)) != 1) || (profile.MedusaV5 && v5Branch(q[:], bodyHash[:], ts) == 12) {
			break
		}
		if i == 1023 {
			return "", nil, fmt.Errorf("signature digest branch unavailable")
		}
	}
	headers := map[string]string{"x-khronos": strconv.FormatUint(uint64(ts), 10)}
	if mode == None {
		return query, headers, nil
	}
	if mode != Registration {
		headers["x-helios"] = heliosFor(ts, ent.HeliosRandom, profile.AID, profile.License)
	}
	if mode == Full || mode == Minimal {
		if profile.MedusaV5 {
			medusa, err := buildMedusaV5(strings.TrimRight(origin, "/")+path+"?"+query, body, ts, deviceID, profile.VersionName, ent)
			if err != nil {
				return "", nil, err
			}
			headers["x-medusa"] = medusa
		} else {
			headers["x-medusa"] = assembleMedusa(strings.TrimRight(origin, "/")+path+"?"+query, body, body != nil, ts, deviceID, profile.VersionName, ent)
		}
	}
	if mode == Full || mode == Registration || mode == Passport {
		headers["x-gorgon"] = xGorgon(query, body, body != nil, ts, ent.GorgonRandom)
		headers["x-ss-req-ticket"] = values.Get("_rticket")
		if body != nil {
			headers["x-ss-stub"] = strings.ToUpper(hex.EncodeToString(bodyHash[:]))
		}
		if mode != Registration {
			headers["x-argus"] = base64.StdEncoding.EncodeToString(le32(ts))
			headers["x-ladon"] = base64.StdEncoding.EncodeToString(be32(ts))
		}
	}
	return query, headers, nil
}

func heliosFor(ts, nonce uint32, aid, license int64) string {
	digest := md5Raw(append(le32(nonce), []byte(strconv.FormatInt(aid, 10))...))
	ascii := []byte(hex.EncodeToString(digest[:]))
	words := make([]uint64, 4)
	for i := range words {
		words[i] = little64(ascii[i*8 : i*8+8])
	}
	table := diffuse(words)
	plain := []byte(fmt.Sprintf("%d-%d-%d", ts, license, aid))
	pad := 16 - len(plain)%16
	for i := 0; i < pad; i++ {
		plain = append(plain, byte(pad))
	}
	for i := 0; i < len(plain); i += 16 {
		a, b := little64(plain[i:i+8]), little64(plain[i+8:i+16])
		for _, rk := range table[:34] {
			b = rk ^ (a + ror64(b, 8))
			a = b ^ ror64(a, 61)
		}
		putLittle64(plain[i:i+8], a)
		putLittle64(plain[i+8:i+16], b)
	}
	return base64.StdEncoding.EncodeToString(append(le32(nonce), plain...))
}

func profileMD5(data []byte) []byte { d := md5.Sum(data); return d[:] }
