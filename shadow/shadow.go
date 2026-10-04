package shadow

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"ontology/delta"
	"ontology/doc"
)

var (
	ErrInvalid  = errors.New("shadow: invalid argument")
	ErrNotFound = errors.New("shadow: device not found")
	ErrExists   = errors.New("shadow: device already exists")
	ErrVersion  = errors.New("shadow: version mismatch")
	ErrTooLarge = errors.New("shadow: too many leaves")
)

// Side selects desired or reported.
type Side int

const (
	Desired Side = iota
	Reported
)

// Result is returned by an accepted update.
type Result struct {
	NoChange bool
	Version  int64
	Upsert   []delta.Entry
	Remove   []string
	Touched  int
}

type devState struct {
	mu sync.RWMutex

	desired   *doc.Node
	reported  *doc.Node
	desLeaves int
	repLeaves int

	mvDesired  map[string]int64
	mvReported map[string]int64

	ver   int64
	delta *delta.Delta
}

// Service holds devices. It is safe for concurrent use.
type Service struct {
	limit int
	mu    sync.RWMutex
	devs  map[string]*devState
}

// NewService creates a service with the per-document leaf limit L (1..10000).
func NewService(limit int) (*Service, error) {
	if limit < 1 || limit > 10000 {
		return nil, ErrInvalid
	}
	return &Service{limit: limit, devs: map[string]*devState{}}, nil
}

// Create registers an empty device at ver=0.
func (s *Service) Create(dev string) error {
	if dev == "" {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devs[dev]; ok {
		return ErrExists
	}
	s.devs[dev] = &devState{
		desired:    doc.NewObject(),
		reported:   doc.NewObject(),
		mvDesired:  map[string]int64{},
		mvReported: map[string]int64{},
		delta:      delta.New(),
	}
	return nil
}

// UpdateDesired merges patch into the device's desired document.
func (s *Service) UpdateDesired(dev string, patch *doc.Node, expect int64) (Result, error) {
	return s.update(dev, patch, expect, Desired)
}

// UpdateReported merges patch into the device's reported document.
func (s *Service) UpdateReported(dev string, patch *doc.Node, expect int64) (Result, error) {
	return s.update(dev, patch, expect, Reported)
}

func (s *Service) update(dev string, patch *doc.Node, expect int64, side Side) (Result, error) {
	// Validation order: arguments > device existence > version > leaf limit.
	// Patch construction (shape validation) happens before any state lookup.
	p, err := doc.NewPatch(patch)
	if err != nil {
		return Result{}, ErrInvalid
	}
	if dev == "" {
		return Result{}, ErrInvalid
	}
	s.mu.RLock()
	d := s.devs[dev]
	s.mu.RUnlock()
	if d == nil {
		return Result{}, ErrNotFound
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if expect != 0 && expect != d.ver {
		return Result{}, ErrVersion
	}

	target := d.desired
	mv := d.mvDesired
	leafCount := d.desLeaves
	if side == Reported {
		target = d.reported
		mv = d.mvReported
		leafCount = d.repLeaves
	}

	newVer := d.ver + 1

	// Collect candidate paths from the post-merge touched subtrees (Set hooks)
	// and from removed subtrees (Delete hooks, pre-removal leaves).
	var setPaths, deletedLeaves []string
	hooks := doc.Hooks{
		Set: func(path string, _, leaf *doc.Node) {
			setPaths = append(setPaths, path)
			mv[path] = newVer
		},
		Delete: func(path string, old *doc.Node) {
			if old.Kind != doc.Object {
				deletedLeaves = append(deletedLeaves, path)
			} else {
				for _, f := range doc.Flatten(old) {
					deletedLeaves = append(deletedLeaves, joinPath(path, f.Path))
				}
			}
			dropMv(mv, old, path)
		},
	}

	res, merr := doc.Merge(target, p, &leafCount, s.limit, hooks)
	if merr != nil {
		if errors.Is(merr, doc.ErrTooLarge) {
			return Result{}, ErrTooLarge
		}
		return Result{}, ErrInvalid
	}

	if side == Desired {
		d.desLeaves = leafCount
	} else {
		d.repLeaves = leafCount
	}

	if !res.Changed {
		return Result{NoChange: true, Version: d.ver, Touched: res.Touched}, nil
	}

	d.ver = newVer

	// Candidates: touched desired paths plus removed desired leaves; when the
	// reported side moved, every desired leaf under the touched prefixes may
	// enter/leave the difference.
	candidates := collectCandidates(d, side, setPaths, deletedLeaves)
	ch := d.delta.Recompute(d.desired, d.reported, d.mvDesired, candidates)
	_ = ch

	return Result{
		Version: d.ver,
		Upsert:  ch.Upsert,
		Remove:  ch.Remove,
		Touched: res.Touched,
	}, nil
}

// collectCandidates gathers absolute leaf paths that may have changed.
func collectCandidates(d *devState, side Side, setPaths, deletedLeaves []string) []string {
	var cand []string
	cand = append(cand, deletedLeaves...)
	switch side {
	case Desired:
		for _, p := range setPaths {
			cand = append(cand, p)
		}
	case Reported:
		// A reported subtree change affects every desired leaf under the
		// same prefixes. Prefixes are the changed (Set) and removed (Delete)
		// paths, regardless of leaf/object direction.
		cand = append(cand, desiredLeavesUnder(d.desired, append(append([]string{}, setPaths...), deletedLeaves...))...)
	}
	return cand
}

func desiredLeavesUnder(desired *doc.Node, prefixes []string) []string {
	seen := map[string]bool{}
	var collect func(n *doc.Node, rest []string, path string)
	collect = func(n *doc.Node, rest []string, path string) {
		if n == nil {
			return
		}
		if len(rest) == 0 {
			if n.Kind != doc.Object {
				seen[path] = true
				return
			}
			for k, ch := range n.Kids {
				collect(ch, nil, joinPath(path, k))
			}
			return
		}
		if n.Kind != doc.Object {
			return
		}
		next := rest[0]
		np := next
		if path != "" {
			np = path + "." + next
		}
		collect(n.Kids[next], rest[1:], np)
	}
	for _, prefix := range prefixes {
		collect(desired, strings.Split(prefix, "."), "")
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// dropMv removes metadata for all leaves of a removed/replaced subtree.
func dropMv(mv map[string]int64, old *doc.Node, prefix string) {
	if old == nil || old.Kind != doc.Object {
		delete(mv, prefix)
		return
	}
	for _, f := range doc.Flatten(old) {
		delete(mv, joinPath(prefix, f.Path))
	}
}

func joinPath(prefix, sub string) string {
	if sub == "" {
		return prefix
	}
	return prefix + "." + sub
}

// Version returns a device's current version.
func (s *Service) Version(dev string) (int64, error) {
	s.mu.RLock()
	d := s.devs[dev]
	s.mu.RUnlock()
	if d == nil {
		return 0, ErrNotFound
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.ver, nil
}

// GetDelta returns the full difference in byte-sorted path order.
func (s *Service) GetDelta(dev string) ([]delta.Entry, error) {
	s.mu.RLock()
	d := s.devs[dev]
	s.mu.RUnlock()
	if d == nil {
		return nil, ErrNotFound
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.delta.All(), nil
}

// Snapshot returns copies of the desired/reported document roots and mv maps.
func (s *Service) Snapshot(dev string) (desired, reported *doc.Node, mvDesired, mvReported map[string]int64, ver int64, err error) {
	s.mu.RLock()
	d := s.devs[dev]
	s.mu.RUnlock()
	if d == nil {
		return nil, nil, nil, nil, 0, ErrNotFound
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	copyMV := func(m map[string]int64) map[string]int64 {
		c := make(map[string]int64, len(m))
		for k, v := range m {
			c[k] = v
		}
		return c
	}
	return doc.Clone(d.desired), doc.Clone(d.reported), copyMV(d.mvDesired), copyMV(d.mvReported), d.ver, nil
}
