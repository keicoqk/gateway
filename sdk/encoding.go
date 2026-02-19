package sdk

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
)

// encodeBase64V1 / decodeBase64V1 implement a simple "base64 variant":
// - encode raw JSON with standard base64.StdEncoding
// - then reverse the entire string (slight obfuscation, to distinguish from plain base64)
//
// Note: the gateway expects all HTTP request bodies to use this encoding; no extra header is required.
func encodeBase64V1(plain []byte) string {
	s := base64.StdEncoding.EncodeToString(plain)
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

func decodeBase64V1(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return base64.StdEncoding.DecodeString(string(r))
}

// decodeRequestBody decodes the entire HTTP body with b64v1 rules.
// Returns an error if decoding fails.
func decodeRequestBody(r *http.Request) ([]byte, error) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	decoded, err := decodeBase64V1(string(raw))
	if err != nil {
		return nil, fmt.Errorf("decode b64v1: %w", err)
	}
	return decoded, nil
}

