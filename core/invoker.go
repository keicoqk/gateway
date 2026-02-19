package core

import (
	"context"
	"fmt"
	"time"

	"github.com/jhump/protoreflect/dynamic/grpcdynamic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Invoker performs gRPC calls using the descriptor directory and target address.
type Invoker struct {
	resolver       *MethodResolver
	inlineResolver *InlineMethodResolver
	timeout        time.Duration
}

// NewInvoker creates an invoker; descriptorDir is the directory containing .pb files, timeout is the per-call gRPC timeout.
func NewInvoker(descriptorDir string, timeout time.Duration) *Invoker {
	return &Invoker{
		resolver:       NewMethodResolver(descriptorDir),
		inlineResolver: NewInlineMethodResolver(),
		timeout:        timeout,
	}
}

// InvokeRequest is the input for the HTTP gateway.
type InvokeRequest struct {
	Target         string // gRPC target address, e.g. "host:port"
	FullMethodName string // v1: full method name

	// v2: inline descriptor (FileDescriptorSet bytes) per request + service/method
	ServiceName         string
	MethodName          string
	InlineDescriptorSet []byte // if non-empty, use this descriptor and write/overwrite cache
	DescriptorID        string // when InlineDescriptorSet is empty, fetch descriptor from cache

	Body []byte // request body as JSON
}

// Invoke performs one Unary gRPC call: Body (JSON) is converted to PB request, target is called, response is converted to JSON.
func (inv *Invoker) Invoke(ctx context.Context, req *InvokeRequest) ([]byte, error) {
	if inv.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, inv.timeout)
		defer cancel()
	}

	var (
		method     = (*ResolvedMethod)(nil)
		methodName string
		err        error
	)
	if len(req.InlineDescriptorSet) > 0 || req.DescriptorID != "" {
		if req.MethodName == "" {
			return nil, fmt.Errorf("missing method for inline descriptor invocation")
		}
		method, _, err = inv.inlineResolver.Resolve(req.InlineDescriptorSet, req.DescriptorID, req.ServiceName, req.MethodName)
		if err != nil {
			return nil, fmt.Errorf("resolve method from inline descriptor: %w", err)
		}
		methodName = "/" + method.ServiceFQN + "/" + method.Method.GetName()
	} else {
		if req.FullMethodName == "" {
			return nil, fmt.Errorf("missing full method name")
		}
		md, err := inv.resolver.Resolve(req.FullMethodName)
		if err != nil {
			return nil, fmt.Errorf("resolve method: %w", err)
		}
		method = &ResolvedMethod{Method: md, ServiceFQN: md.GetService().GetFullyQualifiedName()}
		methodName = req.FullMethodName
	}

	if method.Method.IsClientStreaming() || method.Method.IsServerStreaming() {
		return nil, fmt.Errorf("streaming method not supported: %s", methodName)
	}

	reqMsg, err := JSONToMessage(method.Method, req.Body)
	if err != nil {
		return nil, fmt.Errorf("json to message: %w", err)
	}

	conn, err := grpc.DialContext(ctx, req.Target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", req.Target, err)
	}
	defer conn.Close()

	stub := grpcdynamic.NewStub(conn)
	respMsg, err := stub.InvokeRpc(ctx, method.Method, reqMsg)
	if err != nil {
		return nil, fmt.Errorf("invoke rpc: %w", err)
	}

	return MessageToJSON(respMsg)
}
