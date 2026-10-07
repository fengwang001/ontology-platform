package store

import (
	"sort"
	"strings"
	"sync"
)

// MemDisk is the shared "stable storage" of MemEngine instances. A crash
// destroys the volatile Store/process but a fresh MemEngine reopened on the
// same MemDisk observes exactly the bytes that were Put before the crash.
type MemDisk struct {
	mu    sync.Mutex
	files map[string][]byte
	log   []string
}

// NewMemDisk creates empty stable storage.
func NewMemDisk() *MemDisk {
	return &MemDisk{files: make(map[string][]byte)}
}

func (d *MemDisk) snapshot() map[string][]byte {
	out := make(map[string][]byte, len(d.files))
	for k, v := range d.files {
		cp := make([]byte, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// Clone returns an independent copy of the disk (for determinism tests:
// two recoveries from the same crashed state must classify identically).
func (d *MemDisk) Clone() *MemDisk {
	d.mu.Lock()
	defer d.mu.Unlock()
	return &MemDisk{files: d.snapshot()}
}

// Keys returns all keys, sorted (test support).
func (d *MemDisk) Keys() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	ks := make([]string, 0, len(d.files))
	for k := range d.files {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// MemEngine is an in-memory Engine used for deterministic fault-injection
// tests. Each Put is an atomic map replacement (the memory model analogue
// of fsync+rename); partial writes cannot be observed.
type MemEngine struct {
	disk *MemDisk
}

// NewMemEngine reopens stable storage d.
func NewMemEngine(d *MemDisk) *MemEngine { return &MemEngine{disk: d} }

func (e *MemEngine) Get(key string) ([]byte, error) {
	e.disk.mu.Lock()
	defer e.disk.mu.Unlock()
	v, ok := e.disk.files[key]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	e.disk.log = append(e.disk.log, "get:"+key)
	return cp, nil
}

func (e *MemEngine) Put(key string, value []byte) error {
	e.disk.mu.Lock()
	defer e.disk.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	e.disk.files[key] = cp
	e.disk.log = append(e.disk.log, "put:"+key)
	return nil
}

func (e *MemEngine) Rename(src, dst string) error {
	e.disk.mu.Lock()
	defer e.disk.mu.Unlock()
	v, ok := e.disk.files[src]
	if !ok {
		return ErrNotFound
	}
	cp := make([]byte, len(v))
	copy(cp, v)
	delete(e.disk.files, src)
	e.disk.files[dst] = cp
	e.disk.log = append(e.disk.log, "rename:"+src+"->"+dst)
	return nil
}

func (e *MemEngine) Delete(key string) error {
	e.disk.mu.Lock()
	defer e.disk.mu.Unlock()
	delete(e.disk.files, key)
	e.disk.log = append(e.disk.log, "delete:"+key)
	return nil
}

// RecordAccesses reports how many batch-owned records were inspected since
// this engine was opened. The counter is reset on reopen, so it bounds the
// work of a single recovery pass independently of total batch history.
func (e *MemEngine) RecordAccesses() int {
	e.disk.mu.Lock()
	defer e.disk.mu.Unlock()
	n := 0
	for _, line := range e.disk.log {
		if hasPrefix(line, "get:b/") || hasPrefix(line, "get:active") {
			n++
		}
	}
	return n
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

// ListKeys returns sorted keys beginning with prefix (journal replay).
func (e *MemEngine) ListKeys(prefix string) []string {
	e.disk.mu.Lock()
	defer e.disk.mu.Unlock()
	var out []string
	for k := range e.disk.files {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
