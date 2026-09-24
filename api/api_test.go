package api_test

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/mvcc"
)

type ref map[string]map[int64]string

func chk(t *testing.T, c bool, f string, a ...any) {
	if !c {
		t.Errorf(f, a...)
	}
}
func (h ref) read(k string, s int64) (string, bool) {
	bv, v := int64(-1), ""
	for ver, val := range h[k] {
		if ver <= s && ver > bv {
			bv, v = ver, val
		}
	}
	return v, bv >= 0
}

// TestEightSteps pins the eight-step NOTES.md derivation on key "k".
func TestEightSteps(t *testing.T) {
	m := mvcc.New()
	m.Write("k", "v1")
	A := m.Snapshot()
	m.Write("k", "v2")
	B := m.Snapshot()
	m.Write("k", "v3")
	v1, f1, _ := m.Read("k", A)
	v2, f2, _ := m.Read("k", B)
	chk(t, A == 1 && B == 2 && m.T() == 3 && f1 && v1 == "v1", "step6/A/t wrong")
	chk(t, f2 && v2 == "v2", "step7 Read@B wrong")
	chk(t, m.Release(A) == nil && m.Collect() == 1, "step8 release/collect")
	v2, f2, _ = m.Read("k", B)
	chk(t, f2 && v2 == "v2", "post-collect Read@B wrong")
}
func TestSnapshotIsolation(t *testing.T) {
	s := api.New()
	s.Write("k", "v1")
	a := s.Snapshot()
	for i := 0; i < 50; i++ {
		s.Write("k", fmt.Sprintf("w%d", i))
	}
	v, f, e := s.Read("k", a)
	chk(t, e == nil && f && v == "v1", "old snapshot moved (%q,%v,%v)", v, f, e)
}
func TestNaiveReference(t *testing.T) {
	plans := [][][2]string{
		{{"k", "a"}, {"k", "b"}, {"k", "c"}},
		{{"k", "a"}, {"p", "1"}, {"k", "b"}, {"p", "2"}},
		{{"k", "a"}, {"p", "1"}, {"q", "x"}, {"k", "b"}},
	}
	for pi, ops := range plans {
		s, h := api.New(), ref{"k": {}, "p": {}, "q": {}}
		for _, op := range ops {
			h[op[0]][s.Write(op[0], op[1])] = op[1]
		}
		head := s.Snapshot()
		for _, snap := range []int64{0, head / 2, head} {
			for _, k := range []string{"k", "p", "q", "z"} {
				wv, wf := h.read(k, snap)
				gv, gf, err := s.Read(k, snap)
				chk(t, err == nil && wf == gf && wv == gv, "plan%d %s@%d (%q,%v)!=(%q,%v)", pi, k, snap, gv, gf, wv, wf)
			}
		}
	}
}
func TestCollectSafety(t *testing.T) {
	s := api.New()
	var snaps []int64
	for i := 0; i < 12; i++ {
		s.Write("k", fmt.Sprintf("v%d", i))
		if i%3 == 0 {
			snaps = append(snaps, s.Snapshot())
		}
	}
	before := map[int64]string{}
	for _, x := range snaps {
		before[x], _, _ = s.Read("k", x)
	}
	s.Collect()
	for _, x := range snaps {
		gv, gf, e := s.Read("k", x)
		chk(t, e == nil && gf && gv == before[x], "active read @%d changed (%q,%v)", x, gv, gf)
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	m := mvcc.New()
	m.Write("k", "v1")
	snap := m.Snapshot()
	m.Write("k", "v2")
	chk(t, m.Release(snap) == nil, "seed release")
	rej := func(fn func() error, want error, tag string) {
		chk(t, errors.Is(fn(), want), "%s wrong error", tag)
		chk(t, errors.Is(fn(), want), "%s not repeatable", tag)
	}
	rej(func() error { _, e := m.Write("", "x"); return e }, mvcc.ErrEmptyKey, "empty")
	rej(func() error { _, _, e := m.Read("k", -1); return e }, mvcc.ErrSnapshotRange, "negative")
	rej(func() error { _, _, e := m.Read("k", m.T()+1); return e }, mvcc.ErrSnapshotRange, "over-t")
	rej(func() error { return m.Release(1 << 30) }, mvcc.ErrSnapshotInactive, "inactive")
	chk(t, errors.Is(m.Release(snap), mvcc.ErrSnapshotInactive), "double-release")
	v, f, _ := m.Read("k", m.T())
	chk(t, f && v == "v2", "state changed after rejections (%q,%v)", v, f)
	_, _, e := mvcc.New().Read("nope", 0)
	chk(t, e == nil, "snapshot 0 on empty store illegal: %v", e)
}
func TestConcurrentOldSnapshotReaders(t *testing.T) {
	s := api.New()
	s.Write("k", "seed")
	old := s.Snapshot()
	want, wf, _ := s.Read("k", old)
	const R, W = 8, 1000
	var wg sync.WaitGroup
	var bad atomic.Int64
	start := make(chan struct{})
	wg.Add(R + 1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < W; i++ {
			s.Write("k", fmt.Sprintf("w%d", i))
		}
	}()
	for r := 0; r < R; r++ {
		go func() {
			defer wg.Done()
			<-start
			v, f, e := s.Read("k", old)
			if e != nil || !f || !wf || v != want {
				bad.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	chk(t, bad.Load() == 0, "%d old-snapshot readers drifted", bad.Load())
}
func TestSelfCheck(t *testing.T) {
	chk(t, api.New().SelfCheck() == nil, "SelfCheck failed")
}
