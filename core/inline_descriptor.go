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
	// pending holds in-progress chunked descriptor uploads, keyed by descriptorID.
	pending map[string]*descriptorSyncState
}

func NewInlineMethodResolver() *InlineMethodResolver {
	return &InlineMethodResolver{
		pools:   make(map[string]*InlineDescriptorPool),
		pending: make(map[string]*descriptorSyncState),
	}
}

const (
	maxDescriptorSyncChunks = 2048
	maxDescriptorSyncBytes  = 32 << 20 // 32MiB
)

type descriptorSyncState struct {
	total    int
	received int
	size     int
	chunks   map[int][]byte
}

func newDescriptorSyncState(total int) *descriptorSyncState {
	return &descriptorSyncState{
		total:  total,
		chunks: make(map[int][]byte, total),
	}
}

func (s *descriptorSyncState) assemble() ([]byte, error) {
	if s.total <= 0 {
		return nil, fmt.Errorf("invalid total chunks: %d", s.total)
	}
	if len(s.chunks) != s.total {
		return nil, fmt.Errorf("incomplete chunks: got %d, want %d", len(s.chunks), s.total)
	}
	out := make([]byte, 0, s.size)
	for i := 0; i < s.total; i++ {
		b, ok := s.chunks[i]
		if !ok {
			return nil, fmt.Errorf("missing chunk index %d", i)
		}
		out = append(out, b...)
	}
	return out, nil
}

// SyncDescriptorChunk accepts one descriptor chunk and, once complete, builds and caches the descriptor pool under descriptorID.
// Chunks are expected to be 0-based indexed: index in [0, total).
//
// If reset is true, any existing cached descriptor (and in-progress sync state) for descriptorID is cleared first.
func (r *InlineMethodResolver) SyncDescriptorChunk(descriptorID string, index, total int, chunk []byte, reset bool) (received int, totalChunks int, done bool, err error) {
	descriptorID = strings.TrimSpace(descriptorID)
	if descriptorID == "" {
		return 0, 0, false, fmt.Errorf("empty descriptor id")
	}
	if total <= 0 {
		return 0, 0, false, fmt.Errorf("invalid total chunks: %d", total)
	}
	if total > maxDescriptorSyncChunks {
		return 0, 0, false, fmt.Errorf("too many chunks: %d (max %d)", total, maxDescriptorSyncChunks)
	}
	if index < 0 || index >= total {
		return 0, 0, false, fmt.Errorf("invalid chunk index %d (total %d)", index, total)
	}
	if len(chunk) == 0 {
		return 0, 0, false, fmt.Errorf("empty chunk")
	}
	if len(chunk) > maxDescriptorSyncBytes {
		return 0, 0, false, fmt.Errorf("chunk too large: %d bytes", len(chunk))
	}

	r.mu.RLock()
	_, alreadyCached := r.pools[descriptorID]
	r.mu.RUnlock()
	if alreadyCached && !reset {
		return total, total, true, nil
	}

	var assembled []byte

	r.mu.Lock()
	if reset {
		delete(r.pools, descriptorID)
	}
	if _, ok := r.pools[descriptorID]; ok {
		// Another goroutine may have cached it after our read lock.
		r.mu.Unlock()
		return total, total, true, nil
	}
	st := r.pending[descriptorID]
	if st == nil {
		st = newDescriptorSyncState(total)
		r.pending[descriptorID] = st
	}
	if st.total != total {
		r.mu.Unlock()
		return 0, 0, false, fmt.Errorf("chunk total mismatch for %q: got %d, want %d", descriptorID, total, st.total)
	}
	if _, ok := st.chunks[index]; !ok {
		if st.size+len(chunk) > maxDescriptorSyncBytes {
			r.mu.Unlock()
			return 0, 0, false, fmt.Errorf("descriptor too large: %d bytes (max %d)", st.size+len(chunk), maxDescriptorSyncBytes)
		}
		// Copy chunk bytes to decouple from caller buffer.
		st.chunks[index] = append([]byte(nil), chunk...)
		st.received++
		st.size += len(chunk)
	}
	received = st.received
	totalChunks = st.total
	done = received == totalChunks
	if done {
		assembled, err = st.assemble()
		if err != nil {
			r.mu.Unlock()
			return received, totalChunks, false, err
		}
	}
	r.mu.Unlock()

	if !done {
		return received, totalChunks, false, nil
	}

	pool, err := newInlineDescriptorPool(assembled)
	if err != nil {
		return received, totalChunks, false, err
	}

	r.mu.Lock()
	r.pools[descriptorID] = pool
	delete(r.pending, descriptorID)
	r.mu.Unlock()

	return totalChunks, totalChunks, true, nil
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
