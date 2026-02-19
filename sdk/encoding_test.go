package sdk

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEncodeDecodeBase64V1_RoundTrip(t *testing.T) {
	plain := []byte(`{"foo":"bar","n":123}`)

	encoded := encodeBase64V1(plain)
	if encoded == string(plain) {
		t.Fatalf("encoded should differ from plain text")
	}

	decoded, err := decodeBase64V1(encoded)
	if err != nil {
		t.Fatalf("decodeBase64V1 error: %v", err)
	}
	if string(decoded) != string(plain) {
		t.Fatalf("round-trip mismatch, got %q, want %q", string(decoded), string(plain))
	}
}

func TestDecodeBase64V1_Empty(t *testing.T) {
	decoded, err := decodeBase64V1("")
	if err != nil {
		t.Fatalf("decodeBase64V1 empty error: %v", err)
	}
	if decoded != nil {
		t.Fatalf("expected nil for empty input, got %q", string(decoded))
	}
}

func TestDecodeRequestBody_UsesB64V1(t *testing.T) {
	plain := []byte(`{"hello":"world"}`)
	encoded := encodeBase64V1(plain)

	r := httptest.NewRequest(http.MethodPost, "/grpc-gateway", bytes.NewBufferString(encoded))
	r.Header.Set("Content-Type", "application/json")

	decoded, err := decodeRequestBody(r)
	if err != nil {
		t.Fatalf("decodeRequestBody error: %v", err)
	}
	if string(decoded) != string(plain) {
		t.Fatalf("decodeRequestBody mismatch, got %q, want %q", string(decoded), string(plain))
	}
}

func TestDecodeRequestBody_InvalidBase64(t *testing.T) {
	// Use a string that is clearly not valid b64v1; expect decodeRequestBody to return an error.
	r := httptest.NewRequest(http.MethodPost, "/grpc-gateway", bytes.NewBufferString("not-a-valid-b64v1"))
	r.Header.Set("Content-Type", "application/json")

	_, err := decodeRequestBody(r)
	if err == nil {
		t.Fatalf("expected error for invalid base64 body, got nil")
	}
}
