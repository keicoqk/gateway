package core

import "encoding/base64"

// embeddedDescriptorSets contains FileDescriptorSet bytes keyed by service name.
// This avoids relying on external .pb files at runtime in minimal deployments.
var embeddedDescriptorSets = map[string][]byte{}

func init() {
	// Generated from example/proto/echo.proto at build time.
	if b, err := base64.StdEncoding.DecodeString("CsgBCgplY2hvLnByb3RvEgRlY2hvIicKC0VjaG9SZXF1ZXN0EhgKB21lc3NhZ2UYASABKAlSB21lc3NhZ2UiKAoMRWNob1Jlc3BvbnNlEhgKB21lc3NhZ2UYASABKAlSB21lc3NhZ2UyPAoLRWNob1NlcnZpY2USLQoERWNobxIRLmVjaG8uRWNob1JlcXVlc3QaEi5lY2hvLkVjaG9SZXNwb25zZUIbWhlnYXRld2F5X3Nkay9leGFtcGxlL3BiO3BiYgZwcm90bzM="); err == nil {
		embeddedDescriptorSets["echo.EchoService"] = b
	}
}

// EmbeddedDescriptorSet returns the embedded FileDescriptorSet bytes for a given service name, if present.
func EmbeddedDescriptorSet(serviceName string) ([]byte, bool) {
	b, ok := embeddedDescriptorSets[serviceName]
	if !ok {
		return nil, false
	}
	// Return a copy to protect internal storage.
	return append([]byte(nil), b...), true
}
