package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Sentinel errors returned by the manager and recovery.
var (
	ErrParam     = errors.New("ErrParam")
	ErrNoFile    = errors.New("ErrNoFile")
	ErrDupFile   = errors.New("ErrDupFile")
	ErrRegress   = errors.New("ErrRegress")
	ErrLogAhead  = errors.New("ErrLogAhead")
	ErrOverlap   = errors.New("ErrOverlap")
	ErrNoCurrent = errors.New("ErrNoCurrent")
	ErrCorrupt   = errors.New("ErrCorrupt")
)

// DefaultThreshold is used by Open, which has no explicit T parameter.
const DefaultThreshold = 8

// RecoverError wraps a manifest error with the index of the offending record.
// errors.Is reports both ErrCorrupt and the concrete cause.
type RecoverError struct {
	Index int
	Cause error
}

func (e *RecoverError) Error() string {
	return fmt.Sprintf("ErrCorrupt: record %d: %v", e.Index, e.Cause)
}

func (e *RecoverError) Unwrap() []error { return []error{ErrCorrupt, e.Cause} }

// Manager manages the active version and the on-disk manifests.
type Manager struct {
	mu            sync.Mutex
	version       Version
	threshold     int
	disk          Disk
	overlapProbes int
}

// New creates a Manager whose rotation threshold is T.
func New(T int) (*Manager, error) {
	if T < 1 {
		return nil, ErrParam
	}
	v := Version{NextFile: 2}
	snap := cloneVersion(v)
	d := Disk{
		Manifests: map[uint64][]Record{
			1: {{IsSnapshot: true, Snapshot: &snap}},
		},
		Current: 1,
	}
	return &Manager{version: v, threshold: T, disk: d}, nil
}

// Apply validates and atomically applies an edit.
func (m *Manager) Apply(e Edit) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	probes := 0
	m.overlapProbes = 0
	nv, err := validateEdit(m.version, e, true, &probes)
	if err != nil {
		return err
	}
	m.version = nv
	m.overlapProbes = probes

	rec := Record{Edit: cloneEditPtr(e)}
	m.disk.Manifests[m.disk.Current] = append(m.disk.Manifests[m.disk.Current], rec)
	if len(m.disk.Manifests[m.disk.Current]) > m.threshold {
		m.rotateLocked()
	}
	return nil
}

// Rotate writes a snapshot manifest and flips CURRENT to it.
func (m *Manager) Rotate() (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rotateLocked(), nil
}

// RotateUnflipped writes a snapshot manifest without flipping CURRENT.
func (m *Manager) RotateUnflipped() (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writeSnapshotLocked(), nil
}

// Disk returns a deep copy of the simulated disk.
func (m *Manager) Disk() Disk {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneDisk(m.disk)
}

// View returns a deep copy of the active version.
func (m *Manager) View() Version {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneVersion(m.version)
}

// Recover replays the CURRENT manifest and returns the recovered version.
func Recover(d Disk) (Version, error) {
	recs, ok := d.Manifests[d.Current]
	if !ok {
		return Version{}, ErrNoCurrent
	}
	if len(recs) == 0 || !recs[0].IsSnapshot || recs[0].Torn || recs[0].Snapshot == nil {
		return Version{}, &RecoverError{Index: 0, Cause: ErrCorrupt}
	}
	v := cloneVersion(*recs[0].Snapshot)
	for i := 1; i < len(recs); i++ {
		rec := recs[i]
		if rec.IsSnapshot {
			return Version{}, &RecoverError{Index: i, Cause: ErrCorrupt}
		}
		if rec.Torn {
			if i == len(recs)-1 {
				break
			}
			return Version{}, &RecoverError{Index: i, Cause: ErrCorrupt}
		}
		if rec.Edit == nil {
			return Version{}, &RecoverError{Index: i, Cause: ErrParam}
		}
		probes := 0
		nv, err := validateEdit(v, *rec.Edit, false, &probes)
		if err != nil {
			return Version{}, &RecoverError{Index: i, Cause: err}
		}
		v = nv
	}
	var maxNum uint64
	for num := range d.Manifests {
		if num > maxNum {
			maxNum = num
		}
	}
	v.NextFile = maxU64(v.NextFile, maxNum+1)
	return v, nil
}

// Open recovers d, installs the version as active, and rotates once.
func Open(d Disk) (*Manager, error) {
	v, err := Recover(d)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		version:   v,
		threshold: DefaultThreshold,
		disk:      cloneDisk(d),
	}
	m.rotateLocked()
	return m, nil
}

// OverlapProbes reports comparisons made during the latest Apply overlap check.
func (m *Manager) OverlapProbes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.overlapProbes
}

// rotateLocked consumes one file number, writes a snapshot and flips CURRENT.
func (m *Manager) rotateLocked() uint64 {
	num := m.writeSnapshotLocked()
	m.disk.Current = num
	return num
}

// writeSnapshotLocked consumes one file number and writes a snapshot manifest
// containing NextFile after the bump. CURRENT is untouched.
func (m *Manager) writeSnapshotLocked() uint64 {
	num := m.version.NextFile
	m.version.NextFile++
	snap := cloneVersion(m.version)
	m.disk.Manifests[num] = []Record{{IsSnapshot: true, Snapshot: &snap}}
	return num
}

// validateEdit runs the ordered checks. When full is false (recovery replay)
// steps four and five are skipped.
func validateEdit(cur Version, e Edit, full bool, probes *int) (Version, error) {
	// Step one: ErrParam.
	if err := checkEditParams(e); err != nil {
		return Version{}, err
	}

	nv := cloneVersion(cur)

	// Step two: ErrNoFile, deletes in order.
	for _, dl := range e.Dels {
		files := nv.Files[dl.Level]
		idx := -1
		for i := range files {
			if files[i].Num == dl.Num {
				idx = i
				break
			}
		}
		if idx < 0 {
			return Version{}, ErrNoFile
		}
		nv.Files[dl.Level] = append(files[:idx], files[idx+1:]...)
	}

	// Step three: ErrDupFile.
	seen := map[uint64]bool{}
	for i := range e.Adds {
		f := e.Adds[i]
		if seen[f.Num] {
			return Version{}, ErrDupFile
		}
		for l := 0; l < NumLevels; l++ {
			for _, ex := range nv.Files[l] {
				if ex.Num == f.Num {
					return Version{}, ErrDupFile
				}
			}
		}
		seen[f.Num] = true
	}

	// Effective NextFile: max(current, given, add num+1).
	effectiveNext := nv.NextFile
	if e.NextFile != nil && *e.NextFile > effectiveNext {
		effectiveNext = *e.NextFile
	}
	for i := range e.Adds {
		if e.Adds[i].Num+1 > effectiveNext {
			effectiveNext = e.Adds[i].Num + 1
		}
	}

	// Step four: ErrRegress.
	if full {
		if e.NextFile != nil && *e.NextFile < cur.NextFile {
			return Version{}, ErrRegress
		}
		if e.LogNumber != nil && *e.LogNumber < cur.LogNumber {
			return Version{}, ErrRegress
		}
		if e.LastSeq != nil && *e.LastSeq < cur.LastSeq {
			return Version{}, ErrRegress
		}

		// Step five: ErrLogAhead.
		if e.LogNumber != nil && *e.LogNumber >= effectiveNext {
			return Version{}, ErrLogAhead
		}
	}

	// Ordering and step six: ErrOverlap for levels 1..6.
	// Every add is inserted at its sorted position and checked only against
	// its immediate predecessor and successor, hence at most 2 probes per add.
	for i := range e.Adds {
		f := cloneFile(e.Adds[i])
		files := nv.Files[f.Level]
		if f.Level == 0 {
			nv.Files[0] = append(files, f)
			continue
		}
		pos := sort.Search(len(files), func(j int) bool {
			c := bytes.Compare(files[j].Smallest, f.Smallest)
			if c != 0 {
				return c > 0
			}
			return files[j].Num > f.Num
		})
		if pos < len(files) {
			*probes++
			if intervalsOverlap(f, files[pos]) {
				return Version{}, ErrOverlap
			}
		}
		if pos > 0 {
			*probes++
			if intervalsOverlap(files[pos-1], f) {
				return Version{}, ErrOverlap
			}
		}
		nv.Files[f.Level] = insertFile(files, pos, f)
	}
	nv.Files[0] = orderLevel(0, nv.Files[0])

	nv.NextFile = effectiveNext
	if e.LogNumber != nil {
		nv.LogNumber = *e.LogNumber
	}
	if e.LastSeq != nil {
		nv.LastSeq = *e.LastSeq
	}
	return nv, nil
}

func insertFile(files []File, pos int, f File) []File {
	files = append(files, File{})
	copy(files[pos+1:], files[pos:])
	files[pos] = f
	return files
}

func checkEditParams(e Edit) error {
	empty := len(e.Adds) == 0 && len(e.Dels) == 0 &&
		e.LogNumber == nil && e.NextFile == nil && e.LastSeq == nil
	if empty {
		return ErrParam
	}
	for i := range e.Adds {
		f := e.Adds[i]
		if f.Level < 0 || f.Level >= NumLevels {
			return ErrParam
		}
		if f.Num == 0 || len(f.Smallest) == 0 || len(f.Largest) == 0 ||
			bytes.Compare(f.Smallest, f.Largest) > 0 {
			return ErrParam
		}
	}
	for i := range e.Dels {
		if e.Dels[i].Level < 0 || e.Dels[i].Level >= NumLevels || e.Dels[i].Num == 0 {
			return ErrParam
		}
	}
	return nil
}

func orderLevel(level int, files []File) []File {
	if level == 0 {
		sort.SliceStable(files, func(i, j int) bool {
			if files[i].Num != files[j].Num {
				return files[i].Num > files[j].Num
			}
			return bytes.Compare(files[i].Smallest, files[j].Smallest) < 0
		})
	} else {
		sort.SliceStable(files, func(i, j int) bool {
			c := bytes.Compare(files[i].Smallest, files[j].Smallest)
			if c != 0 {
				return c < 0
			}
			return files[i].Num < files[j].Num
		})
	}
	return files
}

// intervalsOverlap reports whether two closed key intervals overlap.
// Closed intervals touching end-to-end (prev.Largest == next.Smallest) count
// as overlapping.
func intervalsOverlap(prev, next File) bool {
	return bytes.Compare(prev.Largest, next.Smallest) >= 0
}

func maxU64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func cloneFile(f File) File {
	cp := f
	cp.Smallest = append([]byte(nil), f.Smallest...)
	cp.Largest = append([]byte(nil), f.Largest...)
	return cp
}

func cloneVersion(v Version) Version {
	cp := v
	for l := 0; l < NumLevels; l++ {
		cp.Files[l] = make([]File, len(v.Files[l]))
		for i := range v.Files[l] {
			cp.Files[l][i] = cloneFile(v.Files[l][i])
		}
	}
	return cp
}

func cloneEditPtr(e Edit) *Edit {
	cp := e
	cp.Adds = append([]File(nil), e.Adds...)
	for i := range cp.Adds {
		cp.Adds[i] = cloneFile(e.Adds[i])
	}
	cp.Dels = append([]Del(nil), e.Dels...)
	if e.LogNumber != nil {
		v := *e.LogNumber
		cp.LogNumber = &v
	}
	if e.NextFile != nil {
		v := *e.NextFile
		cp.NextFile = &v
	}
	if e.LastSeq != nil {
		v := *e.LastSeq
		cp.LastSeq = &v
	}
	return &cp
}

func cloneDisk(d Disk) Disk {
	cp := Disk{Current: d.Current, Manifests: map[uint64][]Record{}}
	for num, recs := range d.Manifests {
		out := make([]Record, len(recs))
		for i, r := range recs {
			cr := Record{IsSnapshot: r.IsSnapshot, Torn: r.Torn}
			if r.Snapshot != nil {
				s := cloneVersion(*r.Snapshot)
				cr.Snapshot = &s
			}
			if r.Edit != nil {
				cr.Edit = cloneEditPtr(*r.Edit)
			}
			out[i] = cr
		}
		cp.Manifests[num] = out
	}
	return cp
}
