package mediautil

import (
	"encoding/base64"
	"testing"
)

func TestNormalizeImageURLPreservesSignedQueryAndUnwrapsProxy(t *testing.T) {
	const signed = "https://p3-sign.byteimg.com/tos-cn-i-123/poster.webp?x-expires=1900000000&x-signature=a%2Bb%3D&quality=90"
	if got := NormalizeImageURL(signed); got != signed {
		t.Fatalf("signed URL changed: %q", got)
	}
	proxy := "http://127.0.0.1:12345/img?u=" + base64.URLEncoding.EncodeToString([]byte(signed))
	if got := NormalizeImageURL(proxy); got != signed {
		t.Fatalf("proxy URL unwrapped to %q, want %q", got, signed)
	}
	if ImageCacheKey(proxy) != ImageCacheKey(signed) {
		t.Fatal("proxy and canonical URL use different cache entries")
	}
}

func TestNormalizeImageURLUnwrapsAlreadyEscapedProxyParameter(t *testing.T) {
	const signed = "https://p3-sign.byteimg.com/poster.jpg?signature=a%2Fb%3D&expires=1900000000"
	encoded := base64.URLEncoding.EncodeToString([]byte(signed))
	proxy := "http://127.0.0.1:4321/img?u=" + encoded
	if got := NormalizeImageURL(proxy); got != signed {
		t.Fatalf("proxy URL = %q, want %q", got, signed)
	}
}

func TestNormalizeImageURLDecodesEscapedOriginalProxyParameter(t *testing.T) {
	const proxy = "http://127.0.0.1:4321/img?u=https%3A%2F%2Fp3-sign.byteimg.com%2Fposter.jpg%3Fsignature%3Da%252Fb%253D%26expires%3D1900000000"
	const want = "https://p3-sign.byteimg.com/poster.jpg?signature=a%2Fb%3D&expires=1900000000"
	if got := NormalizeImageURL(proxy); got != want {
		t.Fatalf("unwrapped URL = %q, want %q", got, want)
	}
}
