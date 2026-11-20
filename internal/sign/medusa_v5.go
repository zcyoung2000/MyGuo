// V5 field layout and cipher tables are reconstructed from the selected
// reference App's arm64 code and embedded metasec image. Reconstruction notes
// and evidence are maintained in Obsidian.
package sign

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
)

func little64(b []byte) uint64       { return binary.LittleEndian.Uint64(b) }
func putLittle64(b []byte, n uint64) { binary.LittleEndian.PutUint64(b, n) }
func v5Branch(query, bodyHash []byte, ts uint32) uint32 {
	return getIV(getIV(getIV(0x20230928, query), bodyHash), le32(ts)) & 15
}

func v5F13(query, bodyHash []byte, ts uint32) ([]byte, error) {
	if v5Branch(query, bodyHash, ts) != 12 {
		return nil, fmt.Errorf("unsupported V5 digest branch")
	}
	const iv uint32 = 0x9d4a97c2
	count := iv + ts
	tt := make([]uint32, 64)
	for i, v := range branch0TT {
		tt[i] = ror32(v, count+1)
	}
	plain := append(append(append([]byte{}, query...), bodyHash...), le32(ts)...)
	for _, v := range []byte{0xfa, 0x45, 0x61, 0xd7} {
		plain = append(plain, byte((uint16(v)|uint16(v)<<8)>>((count+2)&7)))
	}
	plain = append(plain, 0, 0, 0, 0, 0, 0, 1, 0xa0)
	init := make([]uint32, 8)
	for i, v := range branch0Init {
		init[i] = ror32(v, count&31)
	}
	state := mixRounds(append([]uint32{}, init...), [11]uint32{96, 1, 3, 4, 0, 6, 5, 2, 0, 7, 7}, expandWords(plain), tt, iv)
	ret := make([]byte, 32)
	for i := range state {
		binary.BigEndian.PutUint32(ret[i*4:], state[i]+init[i])
	}
	for i := 0; i < 16; i++ {
		ret[i] ^= ret[i+16]
	}
	return append(append([]byte{}, ret[:16]...), le32(sumMD5(ret[:16])|0x20)...), nil
}

func buildMedusaV5(rawURL string, body []byte, ts uint32, deviceID, version string, ent Entropy) (string, error) {
	_, queryText, _ := strings.Cut(rawURL, "?")
	query := sm3Sum([]byte(queryText))
	bodyHash := md5Raw(body)
	if body == nil {
		bodyHash = [16]byte{}
	}
	f13, err := v5F13(query[:], bodyHash[:], ts)
	if err != nil {
		return "", err
	}
	magic := mustHex("2d4b4fca49750d433fb5ae2c226dcc56")
	flags := []byte{0, 0x80, 0, 0, 0, 0, 0, 0}
	nested := append(protoSInt(1, 1074), protoSInt(2, 10)...)
	nested = append(nested, protoSInt(3, 1388734)...)
	nested = append(nested, protoSInt(5, int64(ent.MessageRandom))...)
	nested13 := append(protoSInt(1, int64(ts)), protoSInt(2, -2)...)
	nested13 = append(nested13, protoSInt(4, 200)...)
	env := protoSInt(1, 1400+ent.EnvLaunch%21)
	env = append(env, protoSInt(2, 494471198)...)
	env = append(env, protoSInt(3, 494471192)...)
	env = append(env, protoSInt(5, 14)...)
	env = append(env, protoString(6, "v04.10.03.04-bugfix")...)
	env = append(env, protoSInt(7, 10000+ent.EnvPID%20000)...)
	env = append(env, protoString(8, "xiaomi_8662_64")...)
	env = append(env, protoBytes(12, deviceProto(deviceID))...)
	env = append(env, protoBytes(13, nested13)...)
	env = append(env, protoString(14, version)...)
	for _, p := range [][2]int64{{15, 1610}, {16, 2}, {17, 3310}, {18, 3889278878}, {19, int64(ts)}, {20, int64(ts) + 1}, {22, 1561828021}, {23, 1561828021}} {
		env = append(env, protoSInt(uint32(p[0]), p[1])...)
	}
	msg := protoBytes(1, magic)
	msg = append(msg, protoSInt(2, 5)...)
	msg = append(msg, protoSInt(3, int64(ent.MessageRandom))...)
	for _, p := range []struct {
		tag uint32
		v   string
	}{{4, "8662"}, {5, deviceID}, {6, "646637794"}, {7, version}, {8, "v04.10.03-ml-android"}} {
		msg = append(msg, protoString(p.tag, p.v)...)
	}
	msg = append(msg, protoSInt(9, 0x040a0300)...)
	msg = append(msg, protoBytes(10, flags)...)
	msg = append(msg, protoSInt(12, int64(ts))...)
	msg = append(msg, protoBytes(13, f13)...)
	msg = append(msg, protoBytes(14, query[:6])...)
	msg = append(msg, protoBytes(15, nested)...)
	msg = append(msg, protoString(16, "A7FYrewKZsafeb7tSezALbte7")...)
	msg = append(msg, protoSInt(17, int64(ts))...)
	msg = append(msg, protoString(20, "none")...)
	msg = append(msg, protoSInt(21, 369)...)
	msg = append(msg, protoBytes(23, env)...)
	msg = append(msg, protoString(24, `{"cmr":16777216,"cmr2":16777216,"un_h":1904058224,"vpn":0,"kd":0,"fkd":2875633904,"pd":-1948848863,"dyn":"","do":0,"tk":true}`)...)
	return sealMedusaV5(msg, flags, query[:], f13, ts, ent), nil
}

func sealMedusaV5(msg, flags, query, f13 []byte, ts uint32, ent Entropy) string {
	magic := mustHex("2d4b4fca49750d433fb5ae2c226dcc56")
	key := mustHex("b23c2a470a20d105dc3f8c4438f8272ed46d6c89ffdcdedd5108698d26e9489d")
	hash, seed := keyHash(key, ent.Random)
	encoded := append(append([]byte{}, flags...), xmxor(msg, hash[:])...)
	for i, j := 0, len(encoded)-1; i < j; i, j = i+1, j-1 {
		encoded[i], encoded[j] = encoded[j], encoded[i]
	}
	for i := range encoded {
		encoded[i] ^= seed[(^i)&3]
	}
	check := uint32(0x18000001) | uint32(query[0]&63)<<14 | uint32(f13[0]&63)<<8
	packed := append([]byte{0xca}, le32(ent.PackedRandom)...)
	packed = append(packed, le32(check)...)
	packed = append(packed, encoded...)
	packed = append(packed, byte(ent.Random>>16), byte(ent.Random>>24))
	ciphertext := compactV5(packed, key, ts)
	prefix := append(le32(5), magic...)
	timestamp := le32(ts)
	for i := range prefix {
		prefix[i] ^= timestamp[i&3]
	}
	prefix = append(prefix, byte(ent.Random), byte(ent.Random>>8), 0, 1)
	return base64.StdEncoding.EncodeToString(append(prefix, ciphertext...))
}

func newAESV5(key []byte, ts uint32) aesV3 {
	w := int(ts & 3)
	box := v5Sbox[w*256 : w*256+256]
	initial := make([]byte, 16)
	for i := 0; i < 4; i++ {
		binary.LittleEndian.PutUint32(initial[i*4:], v5Init[w])
	}
	expanded := append(xorBytes(initial, key), make([]byte, 32)...)
	rounds := uint32(8)
	for i := 4; i < 12; i++ {
		at := 4 * (i - 1)
		a, b, c, d := expanded[at], expanded[at+1], expanded[at+2], expanded[at+3]
		if i&3 == 0 {
			t := byte(v5Init[w]>>(rounds&24)) ^ box[b]
			a, b, c, d = t, box[c], box[d], box[a]
		}
		rounds += 2
		expanded[at+4] = a ^ expanded[at-12]
		expanded[at+5] = b ^ expanded[at-11]
		expanded[at+6] = c ^ expanded[at-10]
		expanded[at+7] = d ^ expanded[at-9]
	}
	var keys [12][4]byte
	for i := range keys {
		copy(keys[i][:], expanded[i*4:i*4+4])
	}
	return aesV3{box: box, con2: v5Con[w], order: v5Order[w], keys: keys}
}

func compactV5(source, key []byte, ts uint32) []byte {
	var compact [32]byte
	for i := 0; i < 31 && (i+1)*8 <= len(source); i++ {
		for j := 0; j < 8; j++ {
			compact[i] |= ((source[i*8+j] >> v5Extract[j]) & 1) << v5Deposit[j]
		}
	}
	compact[31] = 1
	aes := newAESV5(profileMD5(key[:16]), ts)
	previous := profileMD5(key[16:32])
	blocks := make([]byte, 0, 32)
	for i := 0; i < 32; i += 16 {
		encrypted := aes.block(xorBytes(compact[i:i+16], previous))
		blocks = append(blocks, encrypted...)
		previous = encrypted
	}
	out := append([]byte{}, source...)
	for i := 0; i < 31 && (i+1)*8 <= len(out); i++ {
		for j := 0; j < 8; j++ {
			mask := byte(1 << v5Extract[j])
			out[i*8+j] = (out[i*8+j] &^ mask) | ((blocks[i]>>v5Deposit[j])&1)<<v5Extract[j]
		}
	}
	return append([]byte{blocks[31]}, out...)
}
