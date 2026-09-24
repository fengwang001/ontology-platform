// Package fileref models the file store together with per-file reference
// counts: how many existing snapshots reference each file. Commits add
// references, expiry decrements them and deletes files reaching zero.
// Registered file names may never be reused. It depends only on snapchain.
package fileref

import (
	"errors"
	"sort"

	"ontology/snapchain"
)

var (
	// ErrNameConflict covers every name rejection: a name was seen before
	// (including deleted names), add/remove contain duplicates, or they
	// intersect.
	ErrNameConflict = errors.New("fileref: file name conflict (duplicate, intersecting or previously seen)")
	// ErrNegativeN is returned when Expire gets a negative N.
	ErrNegativeN = errors.New("fileref: N must not be negative")
)

// Store is the in-memory "file storage": a set of files, per-file reference
// counts and the register of every name ever introduced.
type Store struct {
	files map[string]struct{}
	refs  map[string]int
	seen  map[string]struct{}

	// lastAccess is an unexported work meter for the latest Expire: number
	// of snapshots visited plus number of file references touched. Deletable
	// files are decided purely by reference counts, never by scanning the
	// file sets of retained snapshots, so it stays independent of retained
	// snapshot size. It is intentionally absent from the public API.
	lastAccess int
}

// New returns an empty store.
func New() *Store {
	return &Store{files: map[string]struct{}{}, refs: map[string]int{}, seen: map[string]struct{}{}}
}

// CheckCommit validates every name rule against the register without
// mutating anything.
func (s *Store) CheckCommit(add, remove []string) error {
	addSet := make(map[string]struct{}, len(add))
	for _, f := range add {
		if _, dup := addSet[f]; dup {
			return ErrNameConflict
		}
		addSet[f] = struct{}{}
		if _, old := s.seen[f]; old {
			return ErrNameConflict // names are never reused, even deleted ones
		}
	}
	remSet := make(map[string]struct{}, len(remove))
	for _, f := range remove {
		if _, dup := remSet[f]; dup {
			return ErrNameConflict
		}
		remSet[f] = struct{}{}
		if _, shared := addSet[f]; shared {
			return ErrNameConflict
		}
	}
	return nil
}

// Commit accounts for one newly appended snapshot. add are the brand-new
// files (registered, physically written, reference count 1); inherited are
// the files the new snapshot carries over from the previous current set,
// each gaining one more reference. Removed files keep their count: the old
// current snapshot still exists and still references them. Call only after
// CheckCommit succeeded.
func (s *Store) Commit(add, inherited []string) {
	for _, f := range add {
		s.seen[f] = struct{}{}
		s.files[f] = struct{}{}
		s.refs[f]++
	}
	for _, f := range inherited {
		s.refs[f]++
	}
}

// Expire partitions the chain, then releases one reference per file of each
// expired snapshot, physically deleting every file whose count reaches
// zero. Deleted names come back sorted ascending.
func (s *Store) Expire(ch *snapchain.Chain, N int, T int64) ([]string, error) {
	if N < 0 {
		return nil, ErrNegativeN
	}
	expired := ch.Partition(N, T)
	s.lastAccess = 0
	deleted := []string{}
	for _, sn := range expired {
		s.lastAccess++ // one snapshot visited
		for _, f := range sn.Files {
			s.lastAccess++ // one file reference touched
			s.refs[f]--
			if s.refs[f] == 0 {
				delete(s.refs, f)
				delete(s.files, f)
				deleted = append(deleted, f)
			}
		}
	}
	sort.Strings(deleted)
	return deleted, nil
}

// Files returns the files currently in storage, sorted ascending.
func (s *Store) Files() []string {
	out := make([]string, 0, len(s.files))
	for f := range s.files {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
