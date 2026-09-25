package mgmt_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/mgmt"
)

type ck struct{ t *testing.T }

func (c ck) no(err error) {
	if err != nil {
		c.t.Helper()
		c.t.Fatal(err)
	}
}
func (c ck) ok(b bool) {
	if !b {
		c.t.Helper()
		c.t.Fatal("assert failed")
	}
}

// TestFailureAtomicity：拒绝前后页数/view 数/内容完全不变（不变量4）。
func TestFailureAtomicity(t *testing.T) {
	c := ck{t}
	m := mgmt.NewManager(2, 1)
	v, _ := m.Alloc([]byte{0, 0})
	w, _ := m.Snapshot(v)
	snap := func() [4]int {
		a, _ := m.Read(v, 0)
		b, _ := m.Read(w, 1)
		return [4]int{m.PageCount(), 0, int(a), int(b)}
	}
	before := snap()
	ops := []func() error{
		func() error { return m.Release(mgmt.View{}) },
		func() error { _, e := m.Read(v, 5); return e },
		func() error { _, e := m.Read(mgmt.View{}, 0); return e },
		func() error { _, e := m.Alloc([]byte{0}); return e },
		func() error { return m.Write(v, 0, 1) }, // 拷贝导致超 maxPages
	}
	for _, op := range ops {
		c.ok(op() != nil && snap() == before)
	}
}

// TestErrorsTableDriven：四类哨兵错误可判定、互不相同，被拒后仍可正常使用。
func TestErrorsTableDriven(t *testing.T) {
	c := ck{t}
	m := mgmt.NewManager(2, 1)
	v, _ := m.Alloc([]byte{0, 0})
	_, _ = m.Snapshot(v) // 让 v 被共享，下面 Write 必触发拷贝
	_, eBadSnap := m.Snapshot(mgmt.View{})
	_, eBadRead := m.Read(mgmt.View{}, 0)
	_, eOffRead := m.Read(v, 2)
	_, eBadLen := m.Alloc([]byte{1})
	_, eLimit := m.Alloc([]byte{0, 0})
	cases := []struct {
		err  error
		want error
	}{
		{eBadSnap, mgmt.ErrInvalidView},
		{m.Write(mgmt.View{}, 0, 1), mgmt.ErrInvalidView},
		{eBadRead, mgmt.ErrInvalidView},
		{m.Release(mgmt.View{}), mgmt.ErrInvalidView},
		{m.Write(v, 2, 1), mgmt.ErrOffsetOutOfRange},
		{eOffRead, mgmt.ErrOffsetOutOfRange},
		{eBadLen, mgmt.ErrBadDataLen},
		{eLimit, mgmt.ErrPageLimit},
		{m.Write(v, 0, 1), mgmt.ErrPageLimit},
	}
	seen := map[error]bool{}
	for _, x := range cases {
		c.ok(errors.Is(x.err, x.want))
		seen[x.want] = true
	}
	g, _ := m.Read(v, 0)
	c.ok(len(seen) == 4 && g == 0)
}

// TestConcurrentSnapshotsWrites：并发各自快照+写不同偏移，互不影响、守恒、无泄漏。
func TestConcurrentSnapshotsWrites(t *testing.T) {
	c, N := ck{t}, 64
	m := mgmt.NewManager(N, N+1)
	root, _ := m.Alloc(make([]byte, N))
	vs := make([]mgmt.View, N)
	var wg sync.WaitGroup
	for i := range N {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := m.Snapshot(root)
			if err != nil {
				c.t.Error(err)
				return
			}
			if err := m.Write(v, i, byte(i+1)); err != nil {
				c.t.Error(err)
				return
			}
			vs[i] = v
		}()
	}
	wg.Wait()
	for i := range N {
		for j := range N {
			want := byte(0)
			if i == j {
				want = byte(i + 1)
			}
			g, _ := m.Read(vs[i], j)
			c.ok(g == want)
		}
	}
	c.no(m.Validate())
	for i := range N {
		c.no(m.Release(vs[i]))
	}
	c.no(m.Release(root))
	c.ok(m.PageCount() == 0)
}

// TestAPISelfCheck：对外 SelfCheck 内置八步序列核验四条不变量，必须通过。
func TestAPISelfCheck(t *testing.T) {
	for _, maxP := range []int{2, 10} {
		if err := api.NewManager(4, maxP).SelfCheck(); err != nil {
			t.Fatal(err)
		}
	}
}
