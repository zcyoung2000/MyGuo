package sign

import (
	"bytes"
	"compress/zlib"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"unicode/utf8"
)

var ErrSDI = errors.New("invalid SDI session response")

type SDIToken struct {
	Value  string
	Status int64
}

func protoUInt(tag uint32, n uint64) []byte { return append(protoKey(tag, 0), varint(n)...) }

type wireField struct {
	tag  uint32
	wire byte
	n    uint64
	data []byte
}

func readWire(p []byte) ([]wireField, error) {
	var fields []wireField
	for len(p) > 0 {
		key, n := binary.Uvarint(p)
		if n <= 0 || key>>3 == 0 || key>>3 > 0x1fffffff {
			return nil, ErrSDI
		}
		p = p[n:]
		f := wireField{tag: uint32(key >> 3), wire: byte(key & 7)}
		switch f.wire {
		case 0:
			f.n, n = binary.Uvarint(p)
			if n <= 0 {
				return nil, ErrSDI
			}
			p = p[n:]
		case 2:
			length, n := binary.Uvarint(p)
			if n <= 0 || length > uint64(len(p)-n) {
				return nil, ErrSDI
			}
			p = p[n:]
			f.data = p[:int(length)]
			p = p[int(length):]
		case 1, 5:
			length := 8
			if f.wire == 5 {
				length = 4
			}
			if len(p) < length {
				return nil, ErrSDI
			}
			f.data = p[:length]
			p = p[length:]
		default:
			return nil, ErrSDI
		}
		fields = append(fields, f)
		if len(fields) > 4096 {
			return nil, ErrSDI
		}
	}
	return fields, nil
}

// EncodeSDI uses zlib, the device SDK's XTEA envelope, and AES-CBC. A fresh
// nonce is chosen for every exchange; plaintext and session tokens are not logged.
func EncodeSDI(payload []byte, ticket int64) ([]byte, error) {
	var nonce [5]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	return encodeSDI(payload, ticket, nonce)
}

func encodeSDI(payload []byte, ticket int64, nonce [5]byte) ([]byte, error) {
	if len(payload) > 65536 || ticket < 0 {
		return nil, ErrSDI
	}
	var compressed bytes.Buffer
	z := zlib.NewWriter(&compressed)
	if _, err := z.Write(payload); err != nil {
		return nil, err
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	packed := append(le32(uint32(len(payload))), compressed.Bytes()...)
	packed[5] = 1 // SDK's fixed zlib header 78 01.
	pad := (8 - len(packed)%8) % 8
	salt := (pad - 1) & 3
	inner := make([]byte, len(packed)+pad+8)
	inner[0] = nonce[4]&0xf8 | byte(pad)
	copy(inner[1+salt:], packed)
	checksumAt := 1
	if salt < 2 {
		checksumAt = 1 + salt + len(packed)
	}
	binary.BigEndian.PutUint16(inner[checksumAt:], sdiCRC16(packed))
	sdiXTEA(inner, nonce[:4], false)
	plain := append([]byte{inner[0] ^ 3}, inner...)
	plain = append(plain, nonce[:4]...)
	padAES := 16 - len(plain)%16
	plain = append(plain, bytes.Repeat([]byte{byte(padAES)}, padAES)...)
	block, err := aes.NewCipher(sdiCommonKey[:16])
	if err != nil {
		return nil, err
	}
	cipher.NewCBCEncrypter(block, sdiCommonKey[16:]).CryptBlocks(plain, plain)
	out := protoUInt(1, 0x40400844)
	out = append(out, protoUInt(2, 2)...)
	out = append(out, protoUInt(3, 2)...)
	out = append(out, protoBytes(4, plain)...)
	out = append(out, protoUInt(5, uint64(ticket)*2)...)
	return out, nil
}

func sdiCRC16(p []byte) uint16 {
	var crc uint16
	for _, b := range p {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func sdiXTEA(p, seed []byte, decrypt bool) {
	keyBytes := mustHex("782399bdfacedead3230313030343034")
	var key [4]uint32
	for i := range key {
		key[i] = binary.BigEndian.Uint32(keyBytes[i*4:])
	}
	chain := append(append([]byte{}, seed...), le32(0x20200427)...)
	rounds := 32 + int(binary.LittleEndian.Uint32(seed)%5)*8
	const delta = uint32(0x9e3779b9)
	for at := 0; at < len(p); at += 8 {
		block := p[at : at+8]
		saved := append([]byte{}, block...)
		if !decrypt {
			for i := range block {
				block[i] ^= chain[i]
			}
		}
		a, b := binary.BigEndian.Uint32(block), binary.BigEndian.Uint32(block[4:])
		if decrypt {
			sum := uint32(rounds) * delta
			for i := 0; i < rounds; i++ {
				b -= ((a<<4 ^ a>>5) + a) ^ (sum + key[(sum>>11)&3])
				sum -= delta
				a -= ((b<<4 ^ b>>5) + b) ^ (sum + key[sum&3])
			}
		} else {
			var sum uint32
			for i := 0; i < rounds; i++ {
				a += ((b<<4 ^ b>>5) + b) ^ (sum + key[sum&3])
				sum += delta
				b += ((a<<4 ^ a>>5) + a) ^ (sum + key[(sum>>11)&3])
			}
		}
		binary.BigEndian.PutUint32(block, a)
		binary.BigEndian.PutUint32(block[4:], b)
		if decrypt {
			for i := range block {
				block[i] ^= chain[i]
			}
			copy(chain, saved)
		} else {
			copy(chain, block)
		}
	}
}

func decryptSDI(encrypted []byte) ([]byte, error) {
	if len(encrypted) == 0 || len(encrypted)%16 != 0 || len(encrypted) > 131072 {
		return nil, ErrSDI
	}
	plain := append([]byte{}, encrypted...)
	block, err := aes.NewCipher(sdiCommonKey[:16])
	if err != nil {
		return nil, err
	}
	cipher.NewCBCDecrypter(block, sdiCommonKey[16:]).CryptBlocks(plain, plain)
	pad := int(plain[len(plain)-1])
	if pad < 1 || pad > 16 || pad > len(plain) {
		return nil, ErrSDI
	}
	for _, v := range plain[len(plain)-pad:] {
		if int(v) != pad {
			return nil, ErrSDI
		}
	}
	plain = plain[:len(plain)-pad]
	if len(plain) < 13 || (len(plain)-5)%8 != 0 || plain[0]^plain[1] != 3 {
		return nil, ErrSDI
	}
	inner := plain[1 : len(plain)-4]
	sdiXTEA(inner, plain[len(plain)-4:], true)
	padInner := int(inner[0] & 7)
	salt := (padInner - 1) & 3
	start := 1 + salt
	if start+4 > len(inner) {
		return nil, ErrSDI
	}
	size := binary.LittleEndian.Uint32(inner[start:])
	if size > 65536 {
		return nil, ErrSDI
	}
	end := len(inner) - padInner - 7 + salt
	if start+4 > end || end > len(inner) {
		return nil, ErrSDI
	}
	z, err := zlib.NewReader(bytes.NewReader(inner[start+4 : end]))
	if err != nil {
		return nil, ErrSDI
	}
	defer z.Close()
	decoded, err := io.ReadAll(io.LimitReader(z, int64(size)+1))
	if err != nil || len(decoded) != int(size) {
		return nil, ErrSDI
	}
	return decoded, nil
}

func DecodeSDI(raw []byte) (SDIToken, error) {
	if len(raw) > 131072 {
		return SDIToken{}, ErrSDI
	}
	fields, err := readWire(raw)
	if err != nil {
		return SDIToken{}, err
	}
	var magic, version uint64
	var encrypted []byte
	seen := map[uint32]bool{}
	for _, f := range fields {
		if f.tag != 1 && f.tag != 2 && f.tag != 6 {
			continue
		}
		if seen[f.tag] {
			return SDIToken{}, ErrSDI
		}
		seen[f.tag] = true
		switch f.tag {
		case 1:
			if f.wire != 0 {
				return SDIToken{}, ErrSDI
			}
			magic = f.n
		case 2:
			if f.wire != 0 {
				return SDIToken{}, ErrSDI
			}
			version = f.n
		case 6:
			if f.wire != 2 {
				return SDIToken{}, ErrSDI
			}
			encrypted = f.data
		}
	}
	if magic != 0x40240624 || version != 2 || len(encrypted) == 0 {
		return SDIToken{}, ErrSDI
	}
	plain, err := decryptSDI(encrypted)
	if err != nil {
		return SDIToken{}, err
	}
	fields, err = readWire(plain)
	if err != nil {
		return SDIToken{}, err
	}
	var token SDIToken
	seen = map[uint32]bool{}
	for _, f := range fields {
		if f.tag != 1 && f.tag != 2 {
			continue
		}
		if seen[f.tag] {
			return token, ErrSDI
		}
		seen[f.tag] = true
		if f.tag == 1 {
			if f.wire != 2 || len(f.data) == 0 || len(f.data) > 256 || !utf8.Valid(f.data) {
				return token, ErrSDI
			}
			token.Value = string(f.data)
		} else {
			if f.wire != 0 {
				return token, ErrSDI
			}
			token.Status = int64(f.n>>1) ^ -int64(f.n&1)
		}
	}
	if !seen[1] || !seen[2] || (token.Status != 200 && token.Status != 202) {
		return SDIToken{}, ErrSDI
	}
	return token, nil
}
