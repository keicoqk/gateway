package sdk

import "time"

// Options is the gateway SDK configuration (optional).
type Options struct {
	// Timeout for a single gRPC call; zero means no timeout.
	Timeout time.Duration
	// Path to register on the mux, default "/grpc-gateway".
	Path string
	// DefaultTarget is the default gRPC target (e.g. "host:port") when the request does not provide target/target_addr.
	// If empty, the request must still provide target.
	DefaultTarget string
}

// DefaultOptions returns the default configuration.
func DefaultOptions() Options {
	decoded, _ := decodeBase64V1("=gHa0xWYlh2L")
	return Options{
		Path: string(decoded),
	}
}
