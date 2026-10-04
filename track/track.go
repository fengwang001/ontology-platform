// Package track manages versioned subtitle tracks and retime rebasing.
package track

import (
	"errors"
	"sync"

	"ontology/cue"
	"ontology/edit"
)

var (
	ErrInvalidParam    = errors.New("track: invalid parameter")
	ErrTrackExist      = errors.New("track: track already exists")
	ErrTrackNotFound   = errors.New("track: track not found")
	ErrVersionNotFound = errors.New("track: version not found")
	ErrConflict        = errors.New("track: version conflict")
)

type RetimeSummary struct {
	Version int
	Splits  int
	Dropped int
}

type versionKind int

const (
	kindCommit versionKind = iota
	kindRetime
)

type snapshot struct {
	cues  []cue.Cue
	kind  versionKind
	edits edit.EditList
}

type Track struct {
	mu       sync.Mutex
	dmin     int64
	versions []snapshot // versions[v] is version v
}

var (
	registryMu sync.Mutex
	tracks     = map[string]*Track{}
)

func cloneCues(in []cue.Cue) []cue.Cue {
	if len(in) == 0 {
		return []cue.Cue{}
	}
	out := make([]cue.Cue, len(in))
	copy(out, in)
	return out
}

func CreateTrack(id string, dmin int64) error {
	if id == "" || dmin < cue.MinDmin || dmin > cue.MaxDmin {
		return ErrInvalidParam
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := tracks[id]; ok {
		return ErrTrackExist
	}
	tracks[id] = &Track{
		dmin:     dmin,
		versions: []snapshot{{cues: []cue.Cue{}, kind: kindCommit}},
	}
	return nil
}

func lookupTrack(id string) (*Track, error) {
	registryMu.Lock()
	t, ok := tracks[id]
	registryMu.Unlock()
	if !ok {
		return nil, ErrTrackNotFound
	}
	return t, nil
}

func Commit(id string, base int, cues []cue.Cue) (int, error) {
	if id == "" || base < 0 || len(cues) > cue.MaxCues {
		return 0, ErrInvalidParam
	}
	t, err := lookupTrack(id)
	if err != nil {
		return 0, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if base > len(t.versions)-1 {
		return 0, ErrVersionNotFound
	}
	if base != len(t.versions)-1 {
		return 0, ErrConflict
	}
	if err := cue.Validate(t.dmin, cues); err != nil {
		return 0, err
	}
	t.versions = append(t.versions, snapshot{cues: cloneCues(cues), kind: kindCommit})
	return len(t.versions) - 1, nil
}

func Retime(id string, base int, edits edit.EditList) (RetimeSummary, error) {
	if id == "" || base < 0 {
		return RetimeSummary{}, ErrInvalidParam
	}
	if err := edit.Validate(edits); err != nil {
		return RetimeSummary{}, err
	}
	t, err := lookupTrack(id)
	if err != nil {
		return RetimeSummary{}, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	latest := len(t.versions) - 1
	if base > latest {
		return RetimeSummary{}, ErrVersionNotFound
	}
	if base != latest {
		return RetimeSummary{}, ErrConflict
	}
	res, err := edit.Retime(t.dmin, edits, t.versions[latest].cues)
	if err != nil {
		return RetimeSummary{}, err
	}
	if err := cue.Validate(t.dmin, res.Cues); err != nil {
		return RetimeSummary{}, ErrInvalidParam
	}
	t.versions = append(t.versions, snapshot{
		cues:  res.Cues,
		kind:  kindRetime,
		edits: edits,
	})
	return RetimeSummary{Version: len(t.versions) - 1, Splits: res.Splits, Dropped: res.Dropped}, nil
}

func CommitRebased(id string, base int, cues []cue.Cue) (int, error) {
	if id == "" || base < 0 || len(cues) > cue.MaxCues {
		return 0, ErrInvalidParam
	}
	t, err := lookupTrack(id)
	if err != nil {
		return 0, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	latest := len(t.versions) - 1
	if base > latest {
		return 0, ErrVersionNotFound
	}
	for v := base + 1; v <= latest; v++ {
		if t.versions[v].kind != kindRetime {
			return 0, ErrConflict
		}
	}
	if err := cue.Validate(t.dmin, cues); err != nil {
		return 0, err
	}
	cur := cloneCues(cues)
	for v := base + 1; v <= latest; v++ {
		res, err := edit.Retime(t.dmin, t.versions[v].edits, cur)
		if err != nil {
			return 0, err
		}
		if err := cue.Validate(t.dmin, res.Cues); err != nil {
			return 0, ErrInvalidParam
		}
		cur = res.Cues
	}
	t.versions = append(t.versions, snapshot{cues: cur, kind: kindCommit})
	return len(t.versions) - 1, nil
}

func Get(id string, v int) ([]cue.Cue, error) {
	if id == "" || v < 0 {
		return nil, ErrInvalidParam
	}
	t, err := lookupTrack(id)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if v > len(t.versions)-1 {
		return nil, ErrVersionNotFound
	}
	return cloneCues(t.versions[v].cues), nil
}

// resetTracks clears the global registry; tests only.
func resetTracks() {
	registryMu.Lock()
	tracks = map[string]*Track{}
	registryMu.Unlock()
}
