package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveModel is an independent reimplementation of the specification used as
// a differential-testing oracle.
type naiveModel struct {
	v         Version
	threshold int
	disk      Disk
}

func newNaiveModel(T int) *naiveModel {
	v := Version{NextFile: 2}
	snap := naiveCloneVersion(v)
	return &naiveModel{
		v:         v,
		threshold: T,
		disk: Disk{
			Manifests: map[uint64][]Record{1: {{IsSnapshot: true, Snapshot: &snap}}},
			Current:   1,
		},
	}
}

func naiveCloneVersion(v Version) Version {
	cp := v
	for l := range cp.Files {
		cp.Files[l] = nil
		for _, f := range v.Files[l] {
			cp.Files[l] = append(cp.Files[l], File{
				Level:    f.Level,
				Num:      f.Num,
				Smallest: append([]byte(nil), f.Smallest...),
				Largest:  append([]byte(nil), f.Largest...),
			})
		}
	}
	return cp
}

func naiveCloneEdit(e Edit) Edit {
	cp := Edit{
		Adds: make([]File, 0, len(e.Adds)),
		Dels: append([]Del(nil), e.Dels...),
	}
	for _, f := range e.Adds {
		cp.Adds = append(cp.Adds, File{
			Level:    f.Level,
			Num:      f.Num,
			Smallest: append([]byte(nil), f.Smallest...),
			Largest:  append([]byte(nil), f.Largest...),
		})
	}
	if e.LogNumber != nil {
		x := *e.LogNumber
		cp.LogNumber = &x
	}
	if e.NextFile != nil {
		x := *e.NextFile
		cp.NextFile = &x
	}
	if e.LastSeq != nil {
		x := *e.LastSeq
		cp.LastSeq = &x
	}
	return cp
}

// naiveApply mirrors Apply: ordered checks, commit, append, rotation.
func (md *naiveModel) naiveApply(e Edit) error {
	if err := naiveCheck(e); err != nil {
		return err
	}
	nv := naiveCloneVersion(md.v)
	for _, dl := range e.Dels {
		idx := -1
		for i, f := range nv.Files[dl.Level] {
			if f.Num == dl.Num {
				idx = i
				break
			}
		}
		if idx < 0 {
			return ErrNoFile
		}
		nv.Files[dl.Level] = append(nv.Files[dl.Level][:idx], nv.Files[dl.Level][idx+1:]...)
	}
	seen := map[uint64]bool{}
	for _, f := range e.Adds {
		if seen[f.Num] {
			return ErrDupFile
		}
		for l := range nv.Files {
			for _, g := range nv.Files[l] {
				if g.Num == f.Num {
					return ErrDupFile
				}
			}
		}
		seen[f.Num] = true
	}
	eff := nv.NextFile
	if e.NextFile != nil && *e.NextFile > eff {
		eff = *e.NextFile
	}
	for _, f := range e.Adds {
		if f.Num+1 > eff {
			eff = f.Num + 1
		}
	}
	if e.NextFile != nil && *e.NextFile < md.v.NextFile {
		return ErrRegress
	}
	if e.LogNumber != nil && *e.LogNumber < md.v.LogNumber {
		return ErrRegress
	}
	if e.LastSeq != nil && *e.LastSeq < md.v.LastSeq {
		return ErrRegress
	}
	if e.LogNumber != nil && *e.LogNumber >= eff {
		return ErrLogAhead
	}
	// Commit files and verify level overlap by full pairwise ordering.
	for _, f := range e.Adds {
		nv.Files[f.Level] = append(nv.Files[f.Level], File{
			Level:    f.Level,
			Num:      f.Num,
			Smallest: append([]byte(nil), f.Smallest...),
			Largest:  append([]byte(nil), f.Largest...),
		})
	}
	naiveSortLevels(nv.Files[:])
	for l := 1; l < NumLevels; l++ {
		for i := 1; i < len(nv.Files[l]); i++ {
			if bytes.Compare(nv.Files[l][i-1].Largest, nv.Files[l][i].Smallest) >= 0 {
				return ErrOverlap
			}
		}
	}
	nv.NextFile = eff
	if e.LogNumber != nil {
		nv.LogNumber = *e.LogNumber
	}
	if e.LastSeq != nil {
		nv.LastSeq = *e.LastSeq
	}
	md.v = nv
	ce := naiveCloneEdit(e)
	md.disk.Manifests[md.disk.Current] = append(md.disk.Manifests[md.disk.Current], Record{Edit: &ce})
	if len(md.disk.Manifests[md.disk.Current]) > md.threshold {
		md.naiveRotate(true)
	}
	return nil
}

func naiveSortLevels(levels [][]File) {
	sort.SliceStable(levels[0], func(i, j int) bool {
		if levels[0][i].Num != levels[0][j].Num {
			return levels[0][i].Num > levels[0][j].Num
		}
		return bytes.Compare(levels[0][i].Smallest, levels[0][j].Smallest) < 0
	})
	for l := 1; l < NumLevels; l++ {
		sort.SliceStable(levels[l], func(i, j int) bool {
			c := bytes.Compare(levels[l][i].Smallest, levels[l][j].Smallest)
			if c != 0 {
				return c < 0
			}
			return levels[l][i].Num < levels[l][j].Num
		})
	}
}

func naiveCheck(e Edit) error {
	if len(e.Adds) == 0 && len(e.Dels) == 0 && e.LogNumber == nil &&
		e.NextFile == nil && e.LastSeq == nil {
		return ErrParam
	}
	for _, f := range e.Adds {
		if f.Level < 0 || f.Level >= NumLevels || f.Num == 0 ||
			len(f.Smallest) == 0 || len(f.Largest) == 0 ||
			bytes.Compare(f.Smallest, f.Largest) > 0 {
			return ErrParam
		}
	}
	for _, dl := range e.Dels {
		if dl.Level < 0 || dl.Level >= NumLevels || dl.Num == 0 {
			return ErrParam
		}
	}
	return nil
}

func (md *naiveModel) naiveRotate(flip bool) uint64 {
	num := md.v.NextFile
	md.v.NextFile++
	snap := naiveCloneVersion(md.v)
	md.disk.Manifests[num] = []Record{{IsSnapshot: true, Snapshot: &snap}}
	if flip {
		md.disk.Current = num
	}
	return num
}

// naiveRecover independently replays a disk exactly like Recover.
func naiveRecover(d Disk) (Version, error) {
	recs, ok := d.Manifests[d.Current]
	if !ok {
		return Version{}, ErrNoCurrent
	}
	if len(recs) == 0 || !recs[0].IsSnapshot || recs[0].Torn || recs[0].Snapshot == nil {
		return Version{}, ErrCorrupt
	}
	v := naiveCloneVersion(*recs[0].Snapshot)
	for i := 1; i < len(recs); i++ {
		rec := recs[i]
		if rec.IsSnapshot {
			return Version{}, ErrCorrupt
		}
		if rec.Torn {
			if i == len(recs)-1 {
				break
			}
			return Version{}, ErrCorrupt
		}
		if rec.Edit == nil {
			return Version{}, ErrParam
		}
		// Replay checks 1,2,3,6 only.
		if err := naiveCheck(*rec.Edit); err != nil {
			return Version{}, err
		}
		nv := naiveCloneVersion(v)
		for _, dl := range rec.Edit.Dels {
			idx := -1
			for j, f := range nv.Files[dl.Level] {
				if f.Num == dl.Num {
					idx = j
					break
				}
			}
			if idx < 0 {
				return Version{}, ErrNoFile
			}
			nv.Files[dl.Level] = append(nv.Files[dl.Level][:idx], nv.Files[dl.Level][idx+1:]...)
		}
		seen := map[uint64]bool{}
		for _, f := range rec.Edit.Adds {
			if seen[f.Num] {
				return Version{}, ErrDupFile
			}
			for l := range nv.Files {
				for _, g := range nv.Files[l] {
					if g.Num == f.Num {
						return Version{}, ErrDupFile
					}
				}
			}
			seen[f.Num] = true
		}
		eff := nv.NextFile
		if rec.Edit.NextFile != nil && *rec.Edit.NextFile > eff {
			eff = *rec.Edit.NextFile
		}
		for _, f := range rec.Edit.Adds {
			if f.Num+1 > eff {
				eff = f.Num + 1
			}
		}
		for _, f := range rec.Edit.Adds {
			nv.Files[f.Level] = append(nv.Files[f.Level], f)
		}
		naiveSortLevels(nv.Files[:])
		for l := 1; l < NumLevels; l++ {
			for j := 1; j < len(nv.Files[l]); j++ {
				if bytes.Compare(nv.Files[l][j-1].Largest, nv.Files[l][j].Smallest) >= 0 {
					return Version{}, ErrOverlap
				}
			}
		}
		nv.NextFile = eff
		if rec.Edit.LogNumber != nil {
			nv.LogNumber = *rec.Edit.LogNumber
		}
		if rec.Edit.LastSeq != nil {
			nv.LastSeq = *rec.Edit.LastSeq
		}
		v = nv
	}
	var maxNum uint64
	for num := range d.Manifests {
		if num > maxNum {
			maxNum = num
		}
	}
	if maxNum+1 > v.NextFile {
		v.NextFile = maxNum + 1
	}
	return v, nil
}

var testKeys = []string{"a", "b", "c", "m", "n", "x", "y", "z"}

func randomEdit(rng *rand.Rand, v Version) Edit {
	var e Edit
	switch rng.Intn(10) {
	case 0:
		e.LogNumber = pu(v.LogNumber + uint64(rng.Intn(3)))
	case 1:
		e.LastSeq = pu(v.LastSeq + uint64(rng.Intn(3)))
	case 2:
		e.NextFile = pu(v.NextFile + uint64(rng.Intn(4)))
	case 3:
		// Explicitly small NextFile near the current value.
		if rng.Intn(2) == 0 {
			e.NextFile = pu(v.NextFile)
		}
	default:
		nAdds := 1 + rng.Intn(2)
		for i := 0; i < nAdds; i++ {
			lo := testKeys[rng.Intn(len(testKeys)-1)]
			hi := testKeys[rng.Intn(len(testKeys))]
			if bytes.Compare([]byte(lo), []byte(hi)) > 0 {
				lo, hi = hi, lo
			}
			var num uint64
			switch rng.Intn(3) {
			case 0:
				num = v.NextFile + uint64(rng.Intn(6))
			case 1:
				if v.NextFile > 2 {
					num = 2 + uint64(rng.Intn(int(v.NextFile-2)))
				} else {
					num = v.NextFile
				}
			default:
				num = v.NextFile
			}
			if num == 0 {
				num = v.NextFile
			}
			e.Adds = append(e.Adds, File{
				Level:    rng.Intn(NumLevels),
				Num:      num,
				Smallest: []byte(lo),
				Largest:  []byte(hi),
			})
		}
	}
	// Maybe delete a live file.
	var live []File
	for l := range v.Files {
		live = append(live, v.Files[l]...)
	}
	if len(live) > 0 && rng.Intn(2) == 0 {
		f := live[rng.Intn(len(live))]
		e.Dels = append(e.Dels, Del{Level: f.Level, Num: f.Num})
	}
	if len(e.Adds) == 0 && len(e.Dels) == 0 && e.LogNumber == nil &&
		e.NextFile == nil && e.LastSeq == nil {
		return randomEdit(rng, v)
	}
	return e
}

func TestRandomDifferential2000(t *testing.T) {
	const groups = 2000
	const seqLen = 15
	var seed int64 = 20261002
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(seed + int64(g)))
		threshold := 1 + rng.Intn(8)
		m, err := New(threshold)
		if err != nil {
			t.Fatal(err)
		}
		md := newNaiveModel(threshold)
		var log []string
		for step := 0; step < seqLen; step++ {
			if rng.Intn(15) == 0 {
				var num1, num2 uint64
				unflipped := rng.Intn(2) == 1
				var err1 error
				if unflipped {
					num1, err1 = m.RotateUnflipped()
					num2 = md.naiveRotate(false)
				} else {
					num1, err1 = m.Rotate()
					num2 = md.naiveRotate(true)
				}
				log = append(log, fmt.Sprintf("step %d rotate(unflipped=%v) got=%d model=%d err=%v",
					step, unflipped, num1, num2, err1))
				if err1 != nil || num1 != num2 {
					t.Fatalf("rotate mismatch:\n%s", joinLog(log))
				}
				continue
			}
			e := randomEdit(rng, m.View())
			ec := naiveCloneEdit(e)
			err1 := m.Apply(e)
			err2 := md.naiveApply(ec)
			verdict := "accepted"
			if err1 != nil {
				verdict = "rejected:" + err1.Error()
			}
			log = append(log, fmt.Sprintf("step %d edit=%s -> %s (model=%v)",
				step, editSummary(ec), verdict, err2))
			if !sameSentinel(err1, err2) {
				t.Fatalf("error mismatch real=%v model=%v\n%s", err1, err2, joinLog(log))
			}
			if !versionsEqual(m.View(), md.v) {
				t.Fatalf("version mismatch\nreal=%+v\nmodel=%+v\n%s", m.View(), md.v, joinLog(log))
			}
			d := m.Disk()
			rv, rerr := Recover(cloneDisk(d))
			mrv, merr := naiveRecover(cloneDisk(d))
			if !sameSentinel(rerr, merr) {
				t.Fatalf("recover error mismatch real=%v model=%v\n%s", rerr, merr, joinLog(log))
			}
			if rerr == nil && !versionsEqual(rv, mrv) {
				t.Fatalf("recover mismatch\nreal=%+v\nmodel=%+v\n%s", rv, mrv, joinLog(log))
			}
			if rerr == nil {
				view := m.View()
				view.NextFile = rv.NextFile
				if rv.NextFile < m.View().NextFile || !versionsEqual(rv, m.View()) {
					t.Fatalf("Recover(Disk()) != View()\nrv=%+v\nview=%+v\n%s", rv, m.View(), joinLog(log))
				}
			}
			if probes := m.OverlapProbes(); probes > 2*len(e.Adds) {
				t.Fatalf("overlapProbes %d > 2*%d\n%s", probes, len(e.Adds), joinLog(log))
			}
		}
		if g < 5 || g%250 == 0 {
			t.Logf("group %d/%d threshold=%d last steps:%s", g, groups, threshold,
				joinLog(lastLines(log, 4)))
		}
	}
}

func joinLog(lines []string) string {
	out := ""
	for _, l := range lines {
		out += "\n  " + l
	}
	return out
}

func lastLines(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

func sameSentinel(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	sentinels := []error{ErrParam, ErrNoFile, ErrDupFile, ErrRegress, ErrLogAhead, ErrOverlap, ErrNoCurrent, ErrCorrupt}
	for _, s := range sentinels {
		if errors.Is(a, s) != errors.Is(b, s) {
			return false
		}
	}
	return true
}

func editSummary(e Edit) string {
	s := "{"
	for _, f := range e.Adds {
		s += fmt.Sprintf("+L%d#%d[%s..%s] ", f.Level, f.Num, f.Smallest, f.Largest)
	}
	for _, dl := range e.Dels {
		s += fmt.Sprintf("-L%d#%d ", dl.Level, dl.Num)
	}
	if e.LogNumber != nil {
		s += fmt.Sprintf("log=%d ", *e.LogNumber)
	}
	if e.NextFile != nil {
		s += fmt.Sprintf("next=%d ", *e.NextFile)
	}
	if e.LastSeq != nil {
		s += fmt.Sprintf("seq=%d ", *e.LastSeq)
	}
	return s + "}"
}
