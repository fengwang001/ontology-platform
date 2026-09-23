package merge

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/posting"
	"ontology/segment"
)

// Set is a concurrency-safe collection of segments plus an in-memory
// buffer. Queries observe one consistent snapshot of the segment set.
type Set struct {
	dir     string
	mu      sync.Mutex
	mem     map[string]*posting.Builder
	nextDoc uint32
	counter int
	snap    atomic.Pointer[snapshot]
}

type snapshot struct {
	segs    []*segment.Segment
	paths   []string
	deleted []map[uint32]bool
}

// Open loads the segment set in dir, cleaning up half-written tmp files
// and orphan segments not listed in the manifest.
func Open(dir string) (*Set, error) {
	s := &Set{dir: dir, mem: map[string]*posting.Builder{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var segsOnDisk []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") {
			os.Remove(filepath.Join(dir, name))
			continue
		}
		if strings.HasSuffix(name, ".seg") {
			segsOnDisk = append(segsOnDisk, name)
		}
	}
	live := segsOnDisk
	if data, err := os.ReadFile(filepath.Join(dir, "MANIFEST")); err == nil {
		live = strings.Fields(string(data))
	}
	onDisk := map[string]bool{}
	for _, n := range segsOnDisk {
		onDisk[n] = true
	}
	snap := &snapshot{}
	maxDoc := int64(-1)
	for _, name := range live {
		if !onDisk[name] {
			continue
		}
		delete(onDisk, name)
		seg, err := segment.Open(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		for _, d := range seg.Docs() {
			if int64(d) > maxDoc {
				maxDoc = int64(d)
			}
		}
		snap.segs = append(snap.segs, seg)
		snap.paths = append(snap.paths, name)
		snap.deleted = append(snap.deleted, map[uint32]bool{})
	}
	for orphan := range onDisk {
		os.Remove(filepath.Join(dir, orphan))
	}
	s.nextDoc = uint32(maxDoc + 1)
	s.counter = len(snap.paths)
	s.snap.Store(snap)
	return s, nil
}

// AddDocument indexes tokens (position = index) and returns the doc ID.
func (s *Set) AddDocument(tokens []string) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc := s.nextDoc
	s.nextDoc++
	for p, tok := range tokens {
		b := s.mem[tok]
		if b == nil {
			b = &posting.Builder{}
			s.mem[tok] = b
		}
		b.Add(doc, uint32(p))
	}
	return doc
}

// Flush writes the in-memory buffer as a new segment.
func (s *Set) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.mem) == 0 {
		return nil
	}
	lists := map[string]posting.List{}
	for t, b := range s.mem {
		lists[t] = b.List()
	}
	name := fmt.Sprintf("seg-%06d.seg", s.counter)
	if err := segment.Write(filepath.Join(s.dir, name), lists); err != nil {
		return err
	}
	s.counter++
	seg, err := segment.Open(filepath.Join(s.dir, name))
	if err != nil {
		return err
	}
	old := s.snap.Load()
	s.snap.Store(&snapshot{
		segs:    append(append([]*segment.Segment{}, old.segs...), seg),
		paths:   append(append([]string{}, old.paths...), name),
		deleted: append(append([]map[uint32]bool{}, old.deleted...), map[uint32]bool{}),
	})
	s.mem = map[string]*posting.Builder{}
	return s.writeManifestLocked()
}

// Delete marks a doc as deleted (filtered in queries, purged on merge).
func (s *Set) Delete(doc uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.snap.Load()
	for i, seg := range old.segs {
		if !slices.Contains(seg.Docs(), doc) {
			continue
		}
		bm := map[uint32]bool{}
		for d := range old.deleted[i] {
			bm[d] = true
		}
		bm[doc] = true
		deleted := append([]map[uint32]bool{}, old.deleted...)
		deleted[i] = bm
		s.snap.Store(&snapshot{segs: old.segs, paths: old.paths, deleted: deleted})
		return
	}
}

// MergeSegments merges all segments into one, purging deleted docs.
// The old segments stay usable until the new snapshot is swapped in.
func (s *Set) MergeSegments() (Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.snap.Load()
	if len(old.segs) == 0 {
		return Stats{}, nil
	}
	name := fmt.Sprintf("seg-%06d.seg", s.counter)
	stats, err := Merge(old.segs, old.deleted, filepath.Join(s.dir, name))
	if err != nil {
		return Stats{}, err
	}
	s.counter++
	seg, err := segment.Open(filepath.Join(s.dir, name))
	if err != nil {
		return Stats{}, err
	}
	s.snap.Store(&snapshot{
		segs:    []*segment.Segment{seg},
		paths:   []string{name},
		deleted: []map[uint32]bool{{}},
	})
	if err := s.writeManifestLocked(); err != nil {
		return stats, err
	}
	for _, p := range old.paths {
		os.Remove(filepath.Join(s.dir, p))
	}
	return stats, nil
}

func (s *Set) writeManifestLocked() error {
	snap := s.snap.Load()
	tmp := filepath.Join(s.dir, "MANIFEST.tmp")
	if err := os.WriteFile(tmp, []byte(strings.Join(snap.paths, "\n")), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, "MANIFEST"))
}
