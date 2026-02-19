package gateway

import "net/http"

func init() {
	// Side-effect registration like pprof: import _ "github.com/keicoqk/gateway/sdk" registers the gateway on http.DefaultServeMux.
	http.Handle(DefaultOptions().Path, Handler(DefaultOptions()))
}

// Register registers the gRPC gateway Handler on mux at opts.Path (default "/grpc-gateway").
// If DefaultServeMux was already registered via import _ "github.com/keicoqk/gateway/sdk", call Register only for a custom mux.
func Register(mux *http.ServeMux, opts Options) {
	if opts.Path == "" {
		opts.Path = DefaultOptions().Path
	}
	mux.Handle(opts.Path, Handler(opts))
}
