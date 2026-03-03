package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/keicoqk/gateway/core"
	pb "github.com/keicoqk/gateway/example/pb"
	"google.golang.org/grpc"
)

type echoServer struct {
	pb.UnimplementedEchoServiceServer
}

func (s echoServer) Echo(_ context.Context, req *pb.EchoRequest) (*pb.EchoResponse, error) {
	return &pb.EchoResponse{Message: req.GetMessage()}, nil
}

func startTestGRPCServer(t *testing.T) (target string, stop func()) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := grpc.NewServer()
	pb.RegisterEchoServiceServer(s, echoServer{})

	go func() {
		_ = s.Serve(lis)
	}()

	return lis.Addr().String(), func() {
		s.Stop()
		_ = lis.Close()
	}
}

func mustReadDescriptor(t *testing.T) []byte {
	t.Helper()

	if b, ok := core.EmbeddedDescriptorSet("echo.EchoService"); ok {
		return b
	}
	t.Fatalf("missing embedded descriptor for echo.EchoService")
	return nil
}

func TestGateway_V2InlineDescriptor(t *testing.T) {
	target, stopGRPC := startTestGRPCServer(t)
	defer stopGRPC()

	descB64 := base64.StdEncoding.EncodeToString(mustReadDescriptor(t))

	h := Handler(Options{
		Timeout: 5 * time.Second,
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	reqBody := map[string]any{
		"target": target,
		// no service; resolve from full method name /echo.EchoService/Echo
		"method":     "/echo.EchoService/Echo",
		"descriptor": descB64,
		"params": map[string]any{
			"message": "Alice",
		},
	}
	raw, _ := json.Marshal(reqBody)
	encoded := encodeBase64V1(raw)

	resp, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(encoded))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status: %d, body: %s", resp.StatusCode, string(b))
	}

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["message"] != "Alice" {
		t.Fatalf("unexpected message: %#v", out["message"])
	}
}

func TestGateway_V1FullMethodCompat(t *testing.T) {
	target, stopGRPC := startTestGRPCServer(t)
	defer stopGRPC()

	h := Handler(Options{
		Timeout: 5 * time.Second,
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	reqBody := map[string]any{
		"target": target,
		"method": "/echo.EchoService/Echo",
		"body": map[string]any{
			"message": "Bob",
		},
	}
	raw, _ := json.Marshal(reqBody)
	encoded := encodeBase64V1(raw)

	resp, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(encoded))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status: %d, body: %s", resp.StatusCode, string(b))
	}

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["message"] != "Bob" {
		t.Fatalf("unexpected message: %#v", out["message"])
	}
}

func TestGateway_EncodedBody_Base64V1(t *testing.T) {
	target, stopGRPC := startTestGRPCServer(t)
	defer stopGRPC()

	descB64 := base64.StdEncoding.EncodeToString(mustReadDescriptor(t))

	h := Handler(Options{
		Timeout: 5 * time.Second,
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	reqBody := map[string]any{
		"target":     target,
		"method":     "/echo.EchoService/Echo",
		"descriptor": descB64,
		"params": map[string]any{
			"message": "Encoded",
		},
	}
	plain, _ := json.Marshal(reqBody)
	encoded := encodeBase64V1(plain)

	req, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewBufferString(encoded))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status: %d, body: %s", resp.StatusCode, string(b))
	}

	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["message"] != "Encoded" {
		t.Fatalf("unexpected message: %#v", out["message"])
	}
}

func TestGateway_V2DescriptorIDOnly(t *testing.T) {
	target, stopGRPC := startTestGRPCServer(t)
	defer stopGRPC()

	desc := mustReadDescriptor(t)
	descB64 := base64.StdEncoding.EncodeToString(desc)

	h := Handler(Options{
		Timeout: 5 * time.Second,
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	// First request: send descriptor + custom descriptor_id, write to cache.
	req1 := map[string]any{
		"target":        target,
		"method":        "/echo.EchoService/Echo",
		"descriptor":    descB64,
		"descriptor_id": "echo-v1",
		"params": map[string]any{
			"message": "First",
		},
	}
	raw1, _ := json.Marshal(req1)
	encoded1 := encodeBase64V1(raw1)
	resp1, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(encoded1))
	if err != nil {
		t.Fatalf("post 1: %v", err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp1.Body)
		t.Fatalf("unexpected status 1: %d, body: %s", resp1.StatusCode, string(b))
	}

	// Second request: only descriptor_id; descriptor is loaded from cache.
	req2 := map[string]any{
		"target":        target,
		"method":        "/echo.EchoService/Echo",
		"descriptor_id": "echo-v1",
		"params": map[string]any{
			"message": "Second",
		},
	}
	raw2, _ := json.Marshal(req2)
	encoded2 := encodeBase64V1(raw2)
	resp2, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(encoded2))
	if err != nil {
		t.Fatalf("post 2: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp2.Body)
		t.Fatalf("unexpected status 2: %d, body: %s", resp2.StatusCode, string(b))
	}

	var out map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&out); err != nil {
		t.Fatalf("decode response 2: %v", err)
	}
	if out["message"] != "Second" {
		t.Fatalf("unexpected message 2: %#v", out["message"])
	}
}

func TestGateway_V2ChunkedDescriptorSync_ThenDescriptorID(t *testing.T) {
	target, stopGRPC := startTestGRPCServer(t)
	defer stopGRPC()

	desc := mustReadDescriptor(t)

	h := Handler(Options{
		Timeout: 5 * time.Second,
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	const (
		descriptorID = "echo-chunk-v1"
		chunkSize    = 80
	)
	totalChunks := (len(desc) + chunkSize - 1) / chunkSize
	if totalChunks < 2 {
		t.Fatalf("expected descriptor to split into >=2 chunks, got %d", totalChunks)
	}

	// Sync descriptor in chunks (no target/method required).
	for i := 0; i < totalChunks; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(desc) {
			end = len(desc)
		}
		chunkB64 := base64.StdEncoding.EncodeToString(desc[start:end])

		req := map[string]any{
			"descriptor_id":          descriptorID,
			"descriptor_chunk_total": totalChunks,
			"descriptor_chunk_index": i,
			"descriptor_chunk":       chunkB64,
		}
		raw, _ := json.Marshal(req)
		encoded := encodeBase64V1(raw)

		resp, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(encoded))
		if err != nil {
			t.Fatalf("post chunk %d: %v", i, err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("unexpected status chunk %d: %d, body: %s", i, resp.StatusCode, string(b))
		}
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("decode chunk %d response: %v, body: %s", i, err, string(b))
		}
		if out["descriptor_id"] != descriptorID {
			t.Fatalf("chunk %d: unexpected descriptor_id: %#v", i, out["descriptor_id"])
		}
		if got := int(out["total_chunks"].(float64)); got != totalChunks {
			t.Fatalf("chunk %d: unexpected total_chunks: %d, want %d", i, got, totalChunks)
		}
		if i < totalChunks-1 {
			if out["done"] != false {
				t.Fatalf("chunk %d: expected done=false, got %#v", i, out["done"])
			}
		} else {
			if out["done"] != true {
				t.Fatalf("chunk %d: expected done=true, got %#v", i, out["done"])
			}
		}
	}

	// Now invoke using only descriptor_id.
	req2 := map[string]any{
		"target":        target,
		"method":        "/echo.EchoService/Echo",
		"descriptor_id": descriptorID,
		"params": map[string]any{
			"message": "AfterSync",
		},
	}
	raw2, _ := json.Marshal(req2)
	encoded2 := encodeBase64V1(raw2)
	resp2, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(encoded2))
	if err != nil {
		t.Fatalf("post invoke: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp2.Body)
		t.Fatalf("unexpected status invoke: %d, body: %s", resp2.StatusCode, string(b))
	}
	var out2 map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&out2); err != nil {
		t.Fatalf("decode invoke response: %v", err)
	}
	if out2["message"] != "AfterSync" {
		t.Fatalf("unexpected message: %#v", out2["message"])
	}
}
