package sign

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// The video-tab route has its own GET envelope in reference App 1.2.31.
// Its ordered query and MD5(empty) are part of the signature. In particular,
// the POST profile's absent-body hash and environment cannot be reused here.
// Reconstruction notes and evidence are maintained in Obsidian under
// hongguo-playback-v1.2.31.
func SignVideoTab(pairs [][2]string, ticket int64, deviceID string) (string, map[string]string, error) {
	return signVideoTab(pairs, ticket, deviceID, drawEntropy(ticket))
}

func signVideoTab(pairs [][2]string, ticket int64, deviceID string, ent Entropy) (string, map[string]string, error) {
	if ticket < 0 || deviceID == "" {
		return "", nil, fmt.Errorf("invalid video-tab signature input")
	}
	ts := uint32(ticket / 1000)
	bodyHash := md5Raw(nil)
	for i := int64(0); i < 1024; i++ {
		ordered := make([]string, 0, len(pairs)+1)
		for _, pair := range pairs {
			if pair[0] != "_rticket" {
				ordered = append(ordered, tabEscape(pair[0])+"="+tabEscape(pair[1]))
			}
		}
		ordered = append(ordered, "_rticket="+strconv.FormatInt(ticket+i, 10))
		query := strings.Join(ordered, "&")
		digest := sm3Sum([]byte(query))
		if v5Branch(digest[:], bodyHash[:], ts) != 12 {
			continue
		}
		f13, err := v5F13(digest[:], bodyHash[:], ts)
		if err != nil {
			return "", nil, err
		}
		process := tabProcess(ticket)
		message := videoTabMessage(deviceID, ts, digest[:], f13, process, ent)
		return query, map[string]string{
			"x-khronos": strconv.FormatUint(uint64(ts), 10),
			"x-helios":  heliosFor(ts, ent.HeliosRandom, 8662, 646637794),
			"x-medusa":  sealMedusaV5(message, make([]byte, 8), digest[:], f13, ts, ent),
		}, nil
	}
	return "", nil, fmt.Errorf("video-tab signature digest branch unavailable")
}

func tabEscape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

type tabProcessInfo struct {
	id       string
	launched int64
}

var videoTabProcess struct {
	sync.Once
	info tabProcessInfo
}

func tabProcess(ticket int64) tabProcessInfo {
	videoTabProcess.Do(func() {
		var random [18]byte
		_, _ = rand.Read(random[:])
		videoTabProcess.info = tabProcessInfo{base64.RawURLEncoding.EncodeToString(random[:]), ticket}
	})
	return videoTabProcess.info
}

func videoTabMessage(deviceID string, ts uint32, query, f13 []byte, process tabProcessInfo, ent Entropy) []byte {
	device := protoSInt(1, 1)
	device = append(device, protoSInt(2, 2)...)
	for _, field := range []struct {
		tag   uint32
		value string
	}{{3, "8662"}, {4, deviceID}, {5, process.id}, {6, "!noperm!"}, {12, "Asia/Shanghai,8"}, {13, "zh_CN"}, {22, "12"}} {
		device = append(device, protoString(field.tag, field.value)...)
	}
	device = append(device, protoSInt(25, process.launched)...)
	for _, field := range []struct {
		tag   uint32
		value string
	}{{30, "PGT-AN10"}, {31, "HONOR"}, {32, "PGT-AN10"}, {34, "HONOR"}} {
		device = append(device, protoString(field.tag, field.value)...)
	}
	env := protoString(6, "v04.10.03.04-bugfix")
	env = append(env, protoString(8, "vivo_8662_64")...)
	env = append(env, protoBytes(12, device)...)
	env = append(env, protoString(14, "7.4.3.32")...)
	var stats []byte
	for _, tag := range []uint32{1, 2, 3, 5} {
		stats = append(stats, protoSInt(tag, 1)...)
	}
	msg := protoBytes(1, mustHex("2d4b4fca49750d433fb5ae2c226dcc56"))
	msg = append(msg, protoSInt(2, 5)...)
	msg = append(msg, protoSInt(3, int64(ent.MessageRandom))...)
	for _, field := range []struct {
		tag   uint32
		value string
	}{{4, "8662"}, {5, deviceID}, {6, "646637794"}, {7, "7.4.3.32"}, {8, "v04.10.03-ml-android"}} {
		msg = append(msg, protoString(field.tag, field.value)...)
	}
	msg = append(msg, protoSInt(9, 0x040a0300)...)
	msg = append(msg, protoBytes(10, make([]byte, 8))...)
	msg = append(msg, protoSInt(12, int64(ts))...)
	msg = append(msg, protoBytes(13, f13)...)
	msg = append(msg, protoBytes(14, query[:6])...)
	msg = append(msg, protoBytes(15, stats)...)
	msg = append(msg, protoSInt(17, int64(ts))...)
	msg = append(msg, protoString(20, "none")...)
	msg = append(msg, protoSInt(21, 377)...)
	msg = append(msg, protoBytes(23, env)...)
	msg = append(msg, protoString(24, `{"cmr":16777216,"cmr2":16777216,"un_h":11,"vpn":0,"kd":0,"dyn":"","do":1,"tk":true}`)...)
	return msg
}
