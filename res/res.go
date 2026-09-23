// Package res provides shared resource accounting and leak detection.
package res

import (
	"os"
	"sync"
)

// Stats is a point-in-time snapshot of the registry counters.
type Stats struct {
	Opens        int // operators successfully opened
	Closes       int // operator Close calls that performed release
	SpillCreated int // spill files created
	SpillRemoved int // spill files deleted
	LiveSpills   int // spill files currently on disk
	PeakRows     int // peak in-memory resident rows reported
}

// Balanced reports the leak invariant: every opened operator was closed and
// every spill file was removed.
func (s Stats) Balanced() bool {
	return s.Opens == s.Closes && s.LiveSpills == 0
}

// Registry is safe for concurrent use by many independent operator trees.
type Registry struct {
	dir  string
	mu   sync.Mutex
	st   Stats
	live map[string]struct{}
}

// NewRegistry creates a registry whose spill files live in dir (empty means
// the OS temp directory).
func NewRegistry(dir string) *Registry {
	if dir == "" {
		dir = os.TempDir()
	}
	return &Registry{dir: dir, live: map[string]struct{}{}}
}

// Opened records one successful operator Open.
func (r *Registry) Opened() {
	r.mu.Lock()
	r.st.Opens++
	r.mu.Unlock()
}

// Closed records one operator Close that actually released resources.
func (r *Registry) Closed() {
	r.mu.Lock()
	r.st.Closes++
	r.mu.Unlock()
}

// NoteRows updates the peak in-memory resident-row counter.
func (r *Registry) NoteRows(n int) {
	r.mu.Lock()
	if n > r.st.PeakRows {
		r.st.PeakRows = n
	}
	r.mu.Unlock()
}

// CreateSpill creates a new temporary spill file on disk.
func (r *Registry) CreateSpill() (*os.File, error) {
	f, err := os.CreateTemp(r.dir, "volcano-spill-*")
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.st.SpillCreated++
	r.st.LiveSpills++
	r.live[f.Name()] = struct{}{}
	r.mu.Unlock()
	return f, nil
}

// RemoveSpill closes (if non-nil) and deletes the spill file. Removing an
// already-removed path is a no-op so Close paths stay idempotent.
func (r *Registry) RemoveSpill(f *os.File) error {
	path := ""
	if f != nil {
		path = f.Name()
		_ = f.Close()
	}
	r.mu.Lock()
	if path == "" {
		r.mu.Unlock()
		return nil
	}
	if _, ok := r.live[path]; !ok {
		r.mu.Unlock()
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		r.mu.Unlock()
		return err
	}
	r.st.SpillRemoved++
	r.st.LiveSpills--
	delete(r.live, path)
	r.mu.Unlock()
	return nil
}

// Snapshot returns a copy of the counters.
func (r *Registry) Snapshot() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.st
}

// Dir reports the spill directory.
func (r *Registry) Dir() string { return r.dir }
