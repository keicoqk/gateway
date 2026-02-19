package gateway

import (
	"net/http"
	"sync"
)

// Register registers the gRPC gateway Handler on mux at opts.Path (default "/grpc-gateway").
// If DefaultServeMux was already registered via import _ "github.com/keicoqk/gateway/sdk", call Register only for a custom mux.
func Register(mux *http.ServeMux) {
	opts := DefaultOptions()
	if opts.Path == "" {
		opts.Path = DefaultOptions().Path
	}
	getRegisterOnce(mux, opts.Path).Do(func() {
		mux.Handle(opts.Path, Handler(opts))
	})
}

var (
	registerOnceMu sync.Mutex
	registerOnce   = map[*http.ServeMux]map[string]*sync.Once{}
)

func getRegisterOnce(mux *http.ServeMux, path string) *sync.Once {
	registerOnceMu.Lock()
	defer registerOnceMu.Unlock()

	byPath, ok := registerOnce[mux]
	if !ok {
		byPath = map[string]*sync.Once{}
		registerOnce[mux] = byPath
	}
	once, ok := byPath[path]
	if !ok {
		once = &sync.Once{}
		byPath[path] = once
	}
	return once
}
