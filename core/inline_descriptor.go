package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/jhump/protoreflect/desc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// ResolvedMethod holds a resolved MethodDescriptor and its service fully-qualified name.
// Used in v2 (inline descriptor per request) to map service short name to actual FQN.
type ResolvedMethod struct {
	Method     *desc.MethodDescriptor
	ServiceFQN string
}

// InlineDescriptorPool is a descriptor pool built from FileDescriptorSet, for looking up MethodDescriptor by service+method.
// It does not rely on on-disk core/*.pb files; suitable for gateway requests with inline single-interface descriptor.
type InlineDescriptorPool struct {
	servicesByFQN  map[string]*desc.ServiceDescriptor
	servicesByName map[string][]*desc.ServiceDescriptor
}

func newInlineDescriptorPool(descriptorSetBytes []byte) (*InlineDescriptorPool, error) {
	var fds descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(descriptorSetBytes, &fds); err != nil {
		return nil, fmt.Errorf("unmarshal FileDescriptorSet: %w", err)
	}
	files, err := desc.CreateFileDescriptorsFromSet(&fds)
	if err != nil {
		return nil, fmt.Errorf("create file descriptors: %w", err)
	}

	pool := &InlineDescriptorPool{
		servicesByFQN:  make(map[string]*desc.ServiceDescriptor),
		servicesByName: make(map[string][]*desc.ServiceDescriptor),
	}
	for _, fd := range files {
		for _, svc := range fd.GetServices() {
			fqn := svc.GetFullyQualifiedName()
			pool.servicesByFQN[fqn] = svc
			pool.servicesByFQN["."+fqn] = svc
			pool.servicesByName[svc.GetName()] = append(pool.servicesByName[svc.GetName()], svc)
		}
	}
	return pool, nil
}

func (p *InlineDescriptorPool) Resolve(service string, method string) (*ResolvedMethod, error) {
	service = strings.TrimSpace(service)
	method = strings.TrimSpace(method)

	// Allow method to be mistakenly passed as "/pkg.Service/Method" and try to correct it
	if strings.Contains(method, "/") {
		svc, m, err := ParseFullMethodName(method)
		if err == nil && service == "" {
			service = svc
			method = m
		}
	}

	service = strings.TrimPrefix(service, "/")
	service = strings.TrimPrefix(service, ".")
	method = strings.TrimPrefix(method, "/")

	if service == "" || method == "" {
		return nil, fmt.Errorf("missing service or method")
	}

	var svc *desc.ServiceDescriptor
	if v, ok := p.servicesByFQN[service]; ok {
		svc = v
	} else if v, ok := p.servicesByFQN["."+service]; ok {
		svc = v
	} else if list := p.servicesByName[service]; len(list) == 1 {
		svc = list[0]
	} else if len(list) > 1 {
		names := make([]string, 0, len(list))
		for _, s := range list {
			names = append(names, s.GetFullyQualifiedName())
		}
		return nil, fmt.Errorf("ambiguous service name %q, candidates: %v", service, names)
	} else {
		return nil, fmt.Errorf("service %q not found in inline descriptor", service)
	}

	md := svc.FindMethodByName(method)
	if md == nil {
		return nil, fmt.Errorf("method %q not found in service %q", method, svc.GetFullyQualifiedName())
	}

	return &ResolvedMethod{Method: md, ServiceFQN: svc.GetFullyQualifiedName()}, nil
}

// InlineMethodResolver caches resolution results of inline descriptors to avoid rebuilding the pool on every request.
type InlineMethodResolver struct {
	mu    sync.RWMutex
	pools map[string]*InlineDescriptorPool
}

func NewInlineMethodResolver() *InlineMethodResolver {
	return &InlineMethodResolver{
		pools: make(map[string]*InlineDescriptorPool),
	}
}

// Resolve resolves the concrete method by descriptor bytes or descriptorID.
// - If descriptorSetBytes is non-empty: use this descriptor and cache it under descriptorID (or sha256 of bytes if empty).
// - If descriptorSetBytes is empty but descriptorID is non-empty: only read the corresponding pool from cache.
func (r *InlineMethodResolver) Resolve(descriptorSetBytes []byte, descriptorID, service, method string) (*ResolvedMethod, string, error) {
	key := descriptorID
	if key == "" && len(descriptorSetBytes) > 0 {
		sum := sha256.Sum256(descriptorSetBytes)
		key = hex.EncodeToString(sum[:])
	}
	if key == "" {
		return nil, "", fmt.Errorf("empty descriptor id")
	}

	r.mu.RLock()
	pool, ok := r.pools[key]
	r.mu.RUnlock()
	if !ok && len(descriptorSetBytes) == 0 {
		return nil, "", fmt.Errorf("descriptor not found for id %q", key)
	}
	if !ok {
		var err error
		pool, err = newInlineDescriptorPool(descriptorSetBytes)
		if err != nil {
			return nil, "", err
		}
		r.mu.Lock()
		// Overwrite/write the latest pool
		r.pools[key] = pool
		r.mu.Unlock()
	}

	rm, err := pool.Resolve(service, method)
	if err != nil {
		return nil, "", err
	}
	return rm, key, nil
}

