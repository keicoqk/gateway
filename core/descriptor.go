package core

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/jhump/protoreflect/desc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// DefaultDescriptorDir returns the directory of the core package (descriptor .pb files live here, shipped with SDK; callers need not generate them).
func DefaultDescriptorDir() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Dir(f)
}

// ParseFullMethodName parses gRPC full method name "/package.Service/Method" into service name "package.Service".
func ParseFullMethodName(fullMethodName string) (serviceName string, methodName string, err error) {
	fullMethodName = strings.TrimPrefix(fullMethodName, "/")
	parts := strings.SplitN(fullMethodName, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid full method name: %q, expected /package.Service/Method", fullMethodName)
	}
	return parts[0], parts[1], nil
}

// MethodResolver resolves and caches *desc.MethodDescriptor by full_method_name.
type MethodResolver struct {
	descriptorDir string
	mu            sync.RWMutex
	cache         map[string]*desc.MethodDescriptor
}

// NewMethodResolver creates a method descriptor resolver; descriptorDir is the directory containing .pb files.
func NewMethodResolver(descriptorDir string) *MethodResolver {
	return &MethodResolver{
		descriptorDir: descriptorDir,
		cache:         make(map[string]*desc.MethodDescriptor),
	}
}

func (r *MethodResolver) Resolve(fullMethodName string) (*desc.MethodDescriptor, error) {
	r.mu.RLock()
	md, ok := r.cache[fullMethodName]
	r.mu.RUnlock()
	if ok {
		return md, nil
	}

	serviceName, _, err := ParseFullMethodName(fullMethodName)
	if err != nil {
		return nil, err
	}

	// Convention: descriptor file name is {service_name}.pb, matching the service name in full_method_name
	pbPath := filepath.Join(r.descriptorDir, serviceName+".pb")
	data, err := os.ReadFile(pbPath)
	if err != nil {
		return nil, fmt.Errorf("read descriptor file %s: %w", pbPath, err)
	}

	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(data, &fds); err != nil {
		return nil, fmt.Errorf("unmarshal FileDescriptorSet: %w", err)
	}

	files, err := desc.CreateFileDescriptorsFromSet(&fds)
	if err != nil {
		return nil, fmt.Errorf("create file descriptors: %w", err)
	}

	// Build gRPC full method name format: /package.Service/Method
	for _, fd := range files {
		for _, svc := range fd.GetServices() {
			for _, m := range svc.GetMethods() {
				fqn := "/" + svc.GetFullyQualifiedName() + "/" + m.GetName()
				if fqn == fullMethodName {
					r.mu.Lock()
					r.cache[fullMethodName] = m
					r.mu.Unlock()
					return m, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("method %q not found in descriptor set", fullMethodName)
}
