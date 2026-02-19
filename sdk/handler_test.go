package sdk

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

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

	// go test runs in sdk/; core/ is one level up.
	b, err := os.ReadFile("../core/echo.EchoService.pb")
	if err != nil {
		t.Fatalf("read descriptor: %v", err)
	}
	return b
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
