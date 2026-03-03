package gateway

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/keicoqk/gateway/core"
)

// JSON structure of the HTTP request body.
//
// v1 (legacy): target + full method name + body
// v2 (new): service + method + inline descriptor + params
type gatewayRequest struct {
	Target            string          `json:"target"`           // gRPC target address, e.g. "host:port"
	TargetAddr        string          `json:"target_addr"`      // same as above, compatibility field
	Method            string          `json:"method"`           // v1: full method name; v2: method name (e.g. CreateUser)
	FullMethodNameAlt string          `json:"full_method_name"` // same as above, compatibility field
	Body              json.RawMessage `json:"body"`             // request body as JSON

	// v2: gateway resolves single-interface descriptor dynamically; no dependency on core/*.pb files.
	// service is optional; if omitted, method must be full name "/package.Service/Method", from which gateway parses service.
	Service      string          `json:"service"`       // service name
	Descriptor   string          `json:"descriptor"`    // base64(FileDescriptorSet bytes)
	DescriptorID string          `json:"descriptor_id"` // logical ID; if only this is sent, use cached descriptor
	Params       json.RawMessage `json:"params"`        // v2 request body JSON (alternative to body)

	// v2: chunked descriptor sync (to avoid oversized request bodies).
	// Chunks are 0-based: index in [0, total).
	DescriptorChunk      string `json:"descriptor_chunk"`       // base64(chunk bytes)
	DescriptorChunkIndex int    `json:"descriptor_chunk_index"` // 0-based index
	DescriptorChunkTotal int    `json:"descriptor_chunk_total"` // total chunks
	DescriptorChunkReset bool   `json:"descriptor_chunk_reset"` // if true, clear existing cache before syncing
}

type errorResponse struct {
	Error string `json:"error"`
}

type descriptorSyncResponse struct {
	DescriptorID   string `json:"descriptor_id"`
	ReceivedChunks int    `json:"received_chunks"`
	TotalChunks    int    `json:"total_chunks"`
	Done           bool   `json:"done"`
}

// Handler returns the gateway http.Handler; descriptors are read from the SDK core package directory (shipped with SDK, callers need not generate).
func Handler(opts Options) http.Handler {
	inv := core.NewInvoker(core.DefaultDescriptorDir(), opts.Timeout)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			// writeJSONError(w, http.StatusMethodNotAllowed, "method must be POST")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		decodedBody, err := decodeRequestBody(r)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			// writeJSONError(w, http.StatusBadRequest, "invalid encoded body: "+err.Error())
			return
		}
		var req gatewayRequest
		if err := json.Unmarshal(decodedBody, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
			return
		}

		// Chunked descriptor sync path: uses the same HTTP endpoint, but does not invoke gRPC.
		// This must run before target/method validation because syncing does not require them.
		if req.DescriptorChunk != "" || req.DescriptorChunkTotal > 0 || req.DescriptorChunkIndex > 0 || req.DescriptorChunkReset {
			if req.DescriptorID == "" {
				writeJSONError(w, http.StatusBadRequest, "missing descriptor_id for descriptor chunk sync")
				return
			}
			if req.DescriptorChunk == "" {
				writeJSONError(w, http.StatusBadRequest, "missing descriptor_chunk for descriptor chunk sync")
				return
			}
			chunkBytes, err := base64.StdEncoding.DecodeString(req.DescriptorChunk)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid base64 descriptor_chunk: "+err.Error())
				return
			}
			received, total, done, err := inv.SyncInlineDescriptorChunk(req.DescriptorID, req.DescriptorChunkIndex, req.DescriptorChunkTotal, chunkBytes, req.DescriptorChunkReset)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "sync descriptor chunk: "+err.Error())
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(descriptorSyncResponse{
				DescriptorID:   req.DescriptorID,
				ReceivedChunks: received,
				TotalChunks:    total,
				Done:           done,
			})
			return
		}

		// target precedence: target > target_addr > opts.DefaultTarget
		target := req.Target
		if target == "" {
			target = req.TargetAddr
		}
		if target == "" {
			target = opts.DefaultTarget
		}
		if target == "" {
			writeJSONError(w, http.StatusBadRequest, "missing target")
			return
		}

		// body or params, default {}
		body := req.Body
		if body == nil {
			body = req.Params
		}
		if body == nil {
			body = []byte("{}")
		}

		// v2: either descriptor or descriptor_id.
		// - If descriptor is provided: use it and update cache to latest;
		// - If only descriptor_id: look up descriptor from cache.
		var invokeReq core.InvokeRequest
		invokeReq.Target = target
		invokeReq.Body = body
		if req.Descriptor != "" {
			if req.Method == "" {
				writeJSONError(w, http.StatusBadRequest, "missing method for inline descriptor request")
				return
			}
			descBytes, err := base64.StdEncoding.DecodeString(req.Descriptor)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid base64 descriptor: "+err.Error())
				return
			}
			invokeReq.ServiceName = req.Service // may be empty; resolved later from method="/pkg.Svc/Method"
			invokeReq.MethodName = req.Method
			invokeReq.InlineDescriptorSet = descBytes
			invokeReq.DescriptorID = req.DescriptorID
		} else if req.DescriptorID != "" {
			if req.Method == "" {
				writeJSONError(w, http.StatusBadRequest, "missing method for descriptor_id request")
				return
			}
			invokeReq.ServiceName = req.Service // may be empty; resolved later from method="/pkg.Svc/Method"
			invokeReq.MethodName = req.Method
			invokeReq.DescriptorID = req.DescriptorID
		} else {
			// v1: full method name (compat full_method_name field)
			fullMethod := req.Method
			if fullMethod == "" {
				fullMethod = req.FullMethodNameAlt
			}
			if fullMethod == "" {
				writeJSONError(w, http.StatusBadRequest, "missing method (full_method_name) or inline descriptor fields")
				return
			}
			invokeReq.FullMethodName = fullMethod
		}

		resp, err := inv.Invoke(r.Context(), &invokeReq)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(resp)
	})
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(errorResponse{Error: msg})
}
