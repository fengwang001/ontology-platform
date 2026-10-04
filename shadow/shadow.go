// Package shadow provides a device shadow service with versioned desired and
// reported documents, per-leaf mv metadata and incremental deltas.
package shadow

import (
	"errors"
	"sync"

	"ontology/delta"
	"ontology/doc"
)

// Sentinel errors.
var (
	ErrNotFound = errors.New("device not found")
	ErrExists   = errors.New("device already exists")
	ErrVersion  = errors.New("version conflict")
	ErrTooLarge = errors.New("too many leaves")
)

// ErrInvalid is the target of errors.Is for all argument errors.
var ErrInvalid error = &invalidError{}

type invalidError struct{ Msg string }

func (e *invalidError) Error() string { return "invalid argument: " + e.Msg }

// Is makes every invalidError match the ErrInvalid sentinel.
func (e *invalidError) Is(target error) bool {
	_, ok := target.(*invalidError)
	return ok
}

// mv is the per-leaf metadata: the version that last set or changed it.
type mv = int

// NoChange reports an update that did not modify the document.
type NoChange struct{}

func (NoChange) Error() string { return "no change" }

// DeltaEntry is re-exported delta.Entry for service callers.
type DeltaEntry = delta.Entry[mv]

// Result is the outcome of an accepted update.
type Result struct {
	Ver    int
	Change delta.Change[mv]
}

// device holds one device's state. Its mutex serializes all updates so the
// observable history is equivalent to some serial order.
type device struct {
	mu             sync.Mutex
	ver            int
	desired        *doc.Node[mv]
	reported       *doc.Node[mv]
	desiredLeaves  int
	reportedLeaves int
	delta          *delta.Set[mv]
}

// Service is the concurrency-safe shadow service.
type Service struct {
	limit int
	mu    sync.RWMutex
	devs  map[string]*device
}

// NewService creates a service with per-document leaf limit L (1..10000).
func NewService(limit int) *Service {
	if limit < 1 || limit > 10000 {
		panic("shadow: leaf limit must be in [1,10000]")
	}
	return &Service{limit: limit, devs: map[string]*device{}}
}

// Create adds a device with two empty documents and ver 0.
func (s *Service) Create(dev string) error {
	if dev == "" {
		return invalidErr("device id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devs[dev]; ok {
		return ErrExists
	}
	s.devs[dev] = &device{
		desired:  doc.NewRoot[mv](),
		reported: doc.NewRoot[mv](),
		delta:    delta.NewSet[mv](),
	}
	return nil
}

// UpdateDesired merges patch into dev's desired document.
func (s *Service) UpdateDesired(dev string, patch map[string]any, expect int) (*Result, error) {
	return s.update(dev, patch, expect, true)
}

// UpdateReported merges patch into dev's reported document.
func (s *Service) UpdateReported(dev string, patch map[string]any, expect int) (*Result, error) {
	return s.update(dev, patch, expect, false)
}

func (s *Service) update(dev string, raw map[string]any, expect int, isDesired bool) (*Result, error) {
	if dev == "" {
		return nil, invalidErr("device id is empty")
	}
	patch, err := doc.ParsePatch(raw)
	if err != nil {
		if di, ok := err.(doc.ErrInvalid); ok {
			return nil, &invalidError{Msg: di.Msg}
		}
		return nil, err
	}
	if expect < 0 {
		return nil, invalidErr("expect must be >= 0")
	}
	s.mu.RLock()
	d := s.devs[dev]
	s.mu.RUnlock()
	if d == nil {
		return nil, ErrNotFound
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if expect != 0 && expect != d.ver {
		return nil, ErrVersion
	}

	target := d.reported
	if isDesired {
		target = d.desired
	}
	ver := d.ver + 1
	rep := doc.Merge(target, patch, func() mv { return ver })
	if rep.LeafCount > s.limit {
		return nil, ErrTooLarge
	}
	if len(rep.Events) == 0 {
		return &Result{Ver: d.ver, Change: delta.Change[mv]{}}, nil
	}

	affected := affectedPaths(rep.Events)
	if isDesired {
		d.desired = rep.Root
		d.desiredLeaves = rep.LeafCount
	} else {
		d.reported = rep.Root
		d.reportedLeaves = rep.LeafCount
	}
	d.ver = ver
	ch := delta.Reconcile(d.delta, d.desired, d.reported, affected)
	return &Result{Ver: ver, Change: ch}, nil
}

// GetDelta returns dev's full delta sorted by path.
func (s *Service) GetDelta(dev string) ([]DeltaEntry, error) {
	if dev == "" {
		return nil, invalidErr("device id is empty")
	}
	s.mu.RLock()
	d := s.devs[dev]
	s.mu.RUnlock()
	if d == nil {
		return nil, ErrNotFound
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.delta.All(), nil
}

// GetVer returns dev's current version.
func (s *Service) GetVer(dev string) (int, error) {
	if dev == "" {
		return 0, invalidErr("device id is empty")
	}
	s.mu.RLock()
	d := s.devs[dev]
	s.mu.RUnlock()
	if d == nil {
		return 0, ErrNotFound
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ver, nil
}

func affectedPaths(evs []doc.Event[mv]) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.Path)
	}
	return out
}

func invalidErr(msg string) error { return &invalidError{Msg: msg} }
