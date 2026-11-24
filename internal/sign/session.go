package sign

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc64"
	"os"
	"strconv"
	"strings"
)

// SessionParams binds the V5 envelope to one immutable device and SDI token.
// The SDI exchange uses Handshake; playback uses the same registered device.
// Protocol reconstruction and original function evidence live in Obsidian.
type SessionParams struct {
	Device    map[string]string
	Token     string
	Counter   uint64
	Handshake bool
}

var sessionCRC = crc64.MakeTable(0x95ac9329ac4bc9b5)

// SignSessionV5 signs the transmitted bytes, including compression. It selects
// a ticket for the supported F13 branch before computing any signature header.
func SignSessionV5(pairs [][2]string, ticket int64, body []byte, p SessionParams) (string, map[string]string, error) {
	if ticket < 0 || p.Device["device_id"] == "" {
		return "", nil, fmt.Errorf("invalid session signature identity")
	}
	for offset := int64(0); offset < 1024; offset++ {
		actual := ticket + offset
		ordered := make([]string, 0, len(pairs))
		for _, pair := range pairs {
			value := pair[1]
			if pair[0] == "_rticket" {
				value = strconv.FormatInt(actual, 10)
			}
			ordered = append(ordered, escapeSession(pair[0])+"="+escapeSession(value))
		}
		query := strings.Join(ordered, "&")
		if !p.Handshake {
			query = strings.Replace(query, "&host_abi=", "&&host_abi=", 1)
		}
		ts := uint32(actual / 1000)
		q := sm3Sum([]byte(query))
		bh := md5Raw(body)
		if v5Branch(q[:], bh[:], ts) != 12 {
			continue
		}
		f13, err := v5F13(q[:], bh[:], ts)
		if err != nil {
			return "", nil, err
		}
		ent := drawEntropy(actual)
		flags := le64(1284)
		if p.Handshake {
			flags = le64(2)
		}
		msg := sessionMessage(p, actual, q[:], f13, flags, ent)
		// The reference's Ladon is a per-request CRC-derived rolling value.
		var crc uint64
		for _, c := range []byte("646637794") {
			crc = sessionCRC[byte(crc)^c] ^ crc>>8
		}
		crc ^= uint64(ent.Random)<<32 | uint64(ent.PackedRandom)&^15
		ladon := uint32(crc>>60 | (uint64(ts)^crc)<<4)
		h := map[string]string{
			"x-khronos":       strconv.FormatUint(uint64(ts), 10),
			"x-argus":         base64.StdEncoding.EncodeToString(le32(ts)),
			"x-ladon":         base64.StdEncoding.EncodeToString(be32(ladon)),
			"x-gorgon":        sessionGorgon(query, body, ts, ent.GorgonRandom, flags),
			"x-helios":        heliosFor(ts, ent.HeliosRandom, 8662, 646637794),
			"x-medusa":        sealMedusaV5(msg, flags, q[:], f13, ts, ent),
			"x-ss-stub":       strings.ToUpper(hex.EncodeToString(bh[:])),
			"x-ss-req-ticket": strconv.FormatInt(actual, 10),
		}
		return query, h, nil
	}
	return "", nil, fmt.Errorf("session signature digest branch unavailable")
}

func escapeSession(s string) string { return tabEscape(s) }
func le64(n uint64) []byte          { b := make([]byte, 8); binary.LittleEndian.PutUint64(b, n); return b }

func sessionGorgon(query string, body []byte, ts, nonce uint32, flags []byte) string {
	q, b := md5Raw([]byte(query)), md5Raw(body)
	in := append(append([]byte{}, q[:4]...), b[:4]...)
	in = append(in, 0, 0, 0, 0)
	in = append(in, le32(0x040a0300)...)
	in = append(in, be32(ts)...)
	key := []byte{0x4a, flags[0], 0x16, byte(nonce >> 8), 0x47, 0x6c, flags[1], byte(nonce)}
	out := rc4Gorgon(in, key)
	for i := range out {
		out[i] = ^(reverseBits(out[(i+1)%len(out)]^(out[i]>>4|out[i]<<4)) ^ 20)
	}
	return hex.EncodeToString(append([]byte{0x84, 0x04, byte(nonce), byte(nonce >> 8), flags[0], flags[1]}, out...))
}

func sessionMessage(p SessionParams, ticket int64, query, f13, flags []byte, ent Entropy) []byte {
	ts := int64(ticket / 1000)
	count := int64(p.Counter)
	if p.Handshake {
		count = 1
	}
	stats := protoSInt(1, count)
	for _, tag := range []uint32{2, 3, 5} {
		stats = append(stats, protoSInt(tag, 694367)...)
	}
	env := protoSInt(1, count)
	env = append(env, protoString(6, "v04.10.03.04-bugfix")...)
	env = append(env, protoSInt(7, int64(os.Getpid()))...)
	env = append(env, protoString(8, "seo_laxin_pc_android")...)
	env = append(env, protoBytes(12, sessionDevice(p.Device, ticket))...)
	// Preserve the explicit empty field 13 used by the reference exchange.
	env = append(env, 0x6a, 0)
	env = append(env, protoString(14, "7.4.3.32")...)
	env = append(env, protoSInt(15, 2114)...)
	env = append(env, protoSInt(19, ticket)...)
	msg := protoBytes(1, mustHex("2d4b4fca49750d433fb5ae2c226dcc56"))
	msg = append(msg, protoSInt(2, 5)...)
	msg = append(msg, protoSInt(3, int64(ent.MessageRandom))...)
	for _, f := range []struct {
		tag uint32
		v   string
	}{{4, "8662"}, {5, p.Device["device_id"]}, {6, "646637794"}, {7, "7.4.3.32"}, {8, "v04.10.03-ml-android"}} {
		msg = append(msg, protoString(f.tag, f.v)...)
	}
	msg = append(msg, protoSInt(9, 0x040a0300)...)
	msg = append(msg, protoBytes(10, flags)...)
	msg = append(msg, protoSInt(12, ts)...)
	msg = append(msg, protoBytes(13, f13)...)
	msg = append(msg, protoBytes(14, query[:6])...)
	msg = append(msg, protoBytes(15, stats)...)
	msg = append(msg, protoString(16, p.Token)...)
	msg = append(msg, protoSInt(17, ts)...)
	msg = append(msg, protoString(20, "none")...)
	msg = append(msg, protoSInt(21, 369)...)
	msg = append(msg, protoBytes(23, env)...)
	msg = append(msg, protoString(24, fmt.Sprintf(`{"cmr":16777216,"cmr2":16777216,"do":0,"dyn":"","kd":0,"tk":%t,"vpn":0}`, p.Token != ""))...)
	return msg
}

func sessionDevice(d map[string]string, ticket int64) []byte {
	hash := md5.Sum([]byte(d["device_id"]))
	text := strings.ToUpper(hex.EncodeToString(hash[:]))
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	id := make([]byte, 26)
	for i := range id {
		id[i] = alphabet[(int(text[i%32])^(i*31))%62]
	}
	launch := ticket - int64(text[4]%30)*86400000
	var out []byte
	for _, f := range []struct {
		tag uint32
		n   int64
	}{{1, 1}, {2, 2}, {7, -888888}, {8, -888888}, {9, 3}, {10, -888888}, {14, 4}, {23, 41}, {24, 36}, {25, launch - 86400000}, {26, launch - 86400000}, {27, launch - 86400000}, {28, launch - 0x5265bfe}, {29, -1}, {38, 31}} {
		out = append(out, protoSInt(f.tag, f.n)...)
	}
	brand := d["device_brand"]
	manufacturer := brand
	if strings.EqualFold(brand, "redmi") || strings.EqualFold(brand, "xiaomi") {
		manufacturer = "Xiaomi"
	}
	for _, f := range []struct {
		tag uint32
		s   string
	}{{3, "8662"}, {4, d["device_id"]}, {5, string(id)}, {6, "!noperm!"}, {11, "!notset!"}, {12, "Asia/Shanghai,8"}, {13, "zh_CN"}, {22, d["os_version"]}, {30, d["device_type"]}, {31, brand}, {32, d["device_type"]}, {33, d["device_type"]}, {34, manufacturer}, {35, brand}, {36, brand}} {
		out = append(out, protoString(f.tag, f.s)...)
	}
	return out
}

// BuildSDIPayload contains device metadata and the previous token. Account
// credentials belong to the HTTP layer and are never encoded in this payload.
func BuildSDIPayload(d map[string]string, previous string) []byte {
	fields := map[uint32][]byte{}
	for _, tag := range []uint32{1, 19, 20, 23, 28, 31, 32, 38, 39, 50, 53, 93, 94, 95, 96, 109, 110, 111, 115, 135, 2000, 2001} {
		fields[tag] = protoString(tag, "!notset!")
	}
	for _, tag := range []uint32{13, 29, 30, 36, 37, 41, 42, 43, 49, 51, 52, 56, 57, 58, 59, 60, 61, 62, 63, 64, 66, 67, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 97, 98, 99, 100, 101, 102, 103, 104, 105, 106, 113, 114, 116, 117, 118} {
		fields[tag] = protoSInt(tag, -999999)
	}
	for _, f := range []struct {
		tag uint32
		s   string
	}{{2, d["device_brand"]}, {3, d["device_type"]}, {5, "Android"}, {6, d["os_version"]}, {7, d["resolution"]}, {9, d["rom_version"]}, {12, "Asia/Shanghai,8"}} {
		fields[f.tag] = protoString(f.tag, f.s)
	}
	for _, f := range []struct {
		tag uint32
		key string
	}{{8, "dpi"}, {48, "os_api"}} {
		n, err := strconv.ParseInt(d[f.key], 10, 64)
		if err != nil {
			n = -999999
		}
		fields[f.tag] = protoSInt(f.tag, n)
	}
	var nested []byte
	for tag := uint32(1); tag <= 2001; tag++ {
		nested = append(nested, fields[tag]...)
	}
	out := protoBytes(1, nested)
	for _, f := range []struct {
		tag uint32
		s   string
	}{{2, previous}, {3, "android"}, {4, "v04.10.03-ml-android"}, {6, "8662"}, {7, "7.4.3.32"}, {8, d["device_id"]}, {17, "!notset!"}} {
		out = append(out, protoString(f.tag, f.s)...)
	}
	for _, f := range []struct {
		tag uint32
		n   int64
	}{{5, 0x040a0300}, {11, -999999}, {16, -999999}} {
		out = append(out, protoSInt(f.tag, f.n)...)
	}
	return out
}
