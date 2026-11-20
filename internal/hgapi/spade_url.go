package hgapi

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

func DecryptSpadeURL(value, seedText string) (string, error) {
	seed, err := base64.StdEncoding.DecodeString(seedText)
	if err != nil || len(seed) == 0 {
		return "", fmt.Errorf("invalid video URL seed")
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) < 20 || raw[0] != 0xa8 || raw[2] != 1 || raw[3] != 0 {
		return "", fmt.Errorf("invalid encrypted video URL")
	}
	constant, _ := hex.DecodeString("4dd4c2e6b83162090e52b3c7a6733ba41cb2462b829ab58a196b39db57177524f49baf7f08e8d68d26a72e37c1a95a2f1f05a51892aef2949732b62a38aadd58")
	h1 := sha512.Sum512(seed)
	h2 := sha512.Sum512(append(h1[:], constant...))
	block, err := aes.NewCipher(h2[:16])
	if err != nil {
		return "", err
	}
	n := (len(raw) - 4) / 16 * 16
	plain := make([]byte, n)
	cipher.NewCBCDecrypter(block, h2[16:32]).CryptBlocks(plain, raw[4:4+n])
	padding := int(plain[len(plain)-1])
	if padding > 0 && padding <= 16 && padding <= len(plain) {
		valid := true
		for _, b := range plain[len(plain)-padding:] {
			if int(b) != padding {
				valid = false
			}
		}
		if valid {
			plain = plain[:len(plain)-padding]
		}
	}
	result := strings.TrimRight(string(plain), "\x00")
	result = strings.ReplaceAll(result, `\/`, "/")
	result = strings.ReplaceAll(result, `\u0026`, "&")
	if !strings.HasPrefix(result, "http://") && !strings.HasPrefix(result, "https://") {
		return "", fmt.Errorf("decrypted video URL is invalid")
	}
	return result, nil
}
