package api

import (
	"errors"
	"math/rand"
	"testing"
)

func ck(t *testing.T, c bool, m string) {
	if !c {
		t.Fatal(m)
	}
}

// 朴素 COW 模拟：共享则先整页拷贝再写、独占原地写。
type simPage struct {
	data []byte
	refs int
}
type naive map[View]*simPage

func (n naive) alloc(v View, d []byte) { n[v] = &simPage{append([]byte(nil), d...), 1} }
func (n naive) snap(nv, v View)        { n[nv] = n[v]; n[nv].refs++ }
func (n naive) release(v View)         { n[v].refs--; delete(n, v) }
func (n naive) write(v View, off int, val byte) {
	p := n[v]
	if p.refs > 1 {
		p.refs--
		p = &simPage{append([]byte(nil), p.data...), 1}
		n[v] = p
	}
	p.data[off] = val
}

// 不变量 1+2：随机操作序列后与朴素参照逐字节、逐计数一致；引用计数守恒且无泄漏。
func TestNaiveEquivalence(t *testing.T) {
	const ps, mp = 8, 64
	for seed := int64(0); seed < 20; seed++ {
		r := rand.New(rand.NewSource(seed))
		a, n := NewManager(ps, mp), naive{}
		var live []View
		for step := 0; step < 500; step++ {
			pick := func() (View, int) { i := r.Intn(len(live)); return live[i], i }
			switch op := r.Intn(4); {
			case op == 0:
				d := make([]byte, ps)
				r.Read(d)
				if v, err := a.Alloc(d); err == nil {
					n.alloc(v, d)
					live = append(live, v)
				}
			case op == 1 && len(live) > 0:
				v, _ := pick()
				if nv, err := a.Snapshot(v); err == nil {
					n.snap(nv, v)
					live = append(live, nv)
				}
			case op == 2 && len(live) > 0:
				v, _ := pick()
				off, val := r.Intn(ps), byte(r.Intn(256))
				err := a.Write(v, off, val)
				if errors.Is(err, ErrTooManyPages) {
					continue // 合法拒绝：两边都不动
				}
				ck(t, err == nil, "write")
				n.write(v, off, val)
			case op == 3 && len(live) > 0:
				v, i := pick()
				live = append(live[:i], live[i+1:]...)
				ck(t, a.Release(v) == nil, "release")
				n.release(v)
			}
		}
		for _, v := range live { // 先全部校验，再统一释放
			ck(t, a.RefCount(v) == n[v].refs, "rc mismatch")
			for off := 0; off < ps; off++ {
				got, _ := a.Read(v, off)
				ck(t, got == n[v].data[off], "content mismatch")
			}
		}
		for _, v := range live {
			ck(t, a.Release(v) == nil, "release")
		}
		ck(t, a.PageCount() == 0, "leak")
	}
}

// 不变量 3：共享页写入不影响其他共享者；独占页原地写只改自己。
func TestWriteIsolation(t *testing.T) {
	a := NewManager(4, 8)
	v0, _ := a.Alloc([]byte{1, 2, 3, 4})
	v1, _ := a.Snapshot(v0)
	ck(t, a.Write(v1, 0, 9) == nil, "write")
	got, _ := a.Read(v0, 0)
	ck(t, got == 1, "shared write polluted other view")
	ck(t, a.Write(v1, 1, 8) == nil, "exclusive write") // 已独占，原地写
	g0, _ := a.Read(v1, 0)
	g1, _ := a.Read(v1, 1)
	ck(t, g0 == 9 && g1 == 8, "own writes lost")
}

// 不变量 4：四类错误互不相同，被拒后状态不变且可继续用。
func TestFailureAtomicity(t *testing.T) {
	a := NewManager(4, 1) // 唯一页槽被占满，任何拷贝/新建都超限
	v0, _ := a.Alloc([]byte{7, 7, 7, 7})
	_, err := a.Snapshot(v0)
	ck(t, err == nil, "snapshot") // 共享同页，不占新页槽
	_, readErr := a.Read(View(9), 0)
	for _, c := range []struct{ got, want error }{
		{a.Write(View(9), 0, 1), ErrUnknownView},
		{readErr, ErrUnknownView},
		{a.Release(View(9)), ErrUnknownView},
		{a.Write(v0, -1, 1), ErrOffsetRange},
		{a.Write(v0, 4, 1), ErrOffsetRange},
		{a.Write(v0, 0, 1), ErrTooManyPages}, // 共享页拷贝会超限
	} {
		ck(t, errors.Is(c.got, c.want), "wrong sentinel")
	}
	_, err = a.Alloc([]byte{1})
	ck(t, errors.Is(err, ErrBadDataLen), "bad len")
	s := []error{ErrUnknownView, ErrOffsetRange, ErrBadDataLen, ErrTooManyPages}
	for i := range s {
		for j := range s {
			ck(t, i == j || s[i] != s[j], "sentinels not distinct")
		}
	}
	got, _ := a.Read(v0, 0)
	ck(t, got == 7 && a.PageCount() == 1 && a.RefCount(v0) == 2, "rejected ops changed state")
}
