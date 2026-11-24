package sign

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestSDIEnvelopePaddingBoundaries(t *testing.T) {
	// Exercise all inner padding and variable XTEA round counts, including
	// an empty payload and the maximum admitted decompressed payload.
	for _, size := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 31, 128, 1024, 65536} {
		for seed := uint32(0); seed < 5; seed++ {
			raw := make([]byte, size)
			for i := range raw {
				raw[i] = byte(i*31 + i/251)
			}
			nonce := [5]byte{byte(seed), 0, 0, 0, 0xa8}
			encoded, err := encodeSDI(raw, 1795492800123, nonce)
			if err != nil {
				t.Fatal(err)
			}
			fields, err := readWire(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if len(fields) != 5 || fields[0].n != 0x40400844 || fields[1].n != 2 || fields[2].n != 2 || fields[4].n != 3590985600246 {
				t.Fatal("incorrect SDI wire header")
			}
			decoded, err := decryptSDI(fields[3].data)
			if err != nil || !bytes.Equal(decoded, raw) {
				t.Fatalf("size=%d seed=%d: %v", size, seed, err)
			}
		}
	}
}

func TestDecodeSDIRequiresValidStatusAndSingleToken(t *testing.T) {
	response := func(payload []byte) []byte {
		envelope, err := encodeSDI(payload, 1795492800123, [5]byte{1, 2, 3, 4, 5})
		if err != nil {
			t.Fatal(err)
		}
		fields, err := readWire(envelope)
		if err != nil {
			t.Fatal(err)
		}
		out := protoUInt(1, 0x40240624)
		out = append(out, protoUInt(2, 2)...)
		return append(out, protoBytes(6, fields[3].data)...)
	}
	for _, status := range []int64{200, 202} {
		payload := append(protoString(1, "fixture-session"), protoSInt(2, status)...)
		token, err := DecodeSDI(response(payload))
		if err != nil || token.Value != "fixture-session" || token.Status != status {
			t.Fatalf("status=%d: %v", status, err)
		}
	}
	for _, payload := range [][]byte{protoString(1, "missing-status"), protoSInt(2, 200), append(protoString(1, "bad-status"), protoSInt(2, 403)...), append(append(protoString(1, "a"), protoString(1, "b")...), protoSInt(2, 200)...)} {
		if _, err := DecodeSDI(response(payload)); !errors.Is(err, ErrSDI) {
			t.Fatal("accepted invalid token response")
		}
	}
	good := response(append(protoString(1, "fixture"), protoSInt(2, 200)...))
	for n := 0; n < len(good); n++ {
		if _, err := DecodeSDI(good[:n]); err == nil {
			t.Fatalf("accepted truncation %d", n)
		}
	}
	if _, err := encodeSDI(make([]byte, 65537), 1, [5]byte{}); !errors.Is(err, ErrSDI) {
		t.Fatal("accepted oversized plaintext")
	}
}

func TestSessionSignatureBindsCompressedBodyAndToken(t *testing.T) {
	d := map[string]string{"device_id": "7100000000000001", "device_brand": "Xiaomi", "device_type": "23127PN0CC", "os_version": "14"}
	query, h, err := SignSessionV5([][2]string{{"device_id", d["device_id"]}, {"_rticket", ""}, {"host_abi", "arm64-v8a"}}, 1795492800123, []byte{0x1f, 0x8b, 8}, SessionParams{Device: d, Token: "session-fixture", Counter: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(h["x-medusa"]) < 128 || len(h["x-gorgon"]) != 52 || query == "" {
		t.Fatal("missing session signatures")
	}
	gorgon, err := hex.DecodeString(h["x-gorgon"])
	if err != nil || binary.LittleEndian.Uint16(gorgon[4:6]) != 1284 {
		t.Fatal("playback action flags missing")
	}
	sum := md5Raw([]byte{0x1f, 0x8b, 8})
	if h["x-ss-stub"] != strings.ToUpper(hex.EncodeToString(sum[:])) {
		t.Fatal("body hash did not bind the transmitted bytes")
	}
}
