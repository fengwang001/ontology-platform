package mgmt

import (
	"math/rand/v2"
	"testing"
)

// C 是断言助手：把「if 失败 { t.Fatal }」压成一次方法调用。
type C struct{ t *testing.T }

func (c C) no(err error) {
	if err != nil {
		c.t.Helper()
		c.t.Fatal(err)
	}
}
func (c C) ok(b bool) {
	if !b {
		c.t.Helper()
		c.t.Fatal("assert failed")
	}
}

func grpSize(grp map[View]int, alive []View, id int) (n int) {
	for _, w := range alive {
		if grp[w] == id {
			n++
		}
	}
	return
}

func without(vs []View, x View) []View {
	out := vs[:0]
	for _, v := range vs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}

// TestNaiveModelEquivalence：随机序列下实现与朴素 COW 参照逐字节一致（不变量1/2）。
func TestNaiveModelEquivalence(t *testing.T) {
	c, rng := C{t}, rand.New(rand.NewPCG(1, 2))
	for range 40 {
		m := NewManager(4, 64)
		v0, _ := m.Alloc(make([]byte, 4))
		grp := map[View]int{v0: 0}       // view→朴素页组
		mem := [][]byte{make([]byte, 4)} // 页组→内容
		alive := []View{v0}
		for range 30 {
			v := alive[rng.IntN(len(alive))]
			switch rng.IntN(4) {
			case 0:
				if len(alive) < 10 {
					nv, _ := m.Snapshot(v)
					grp[nv], alive = grp[v], append(alive, nv)
				}
			case 1: // 参照：先数共享者，>1 先整页拷贝再写
				off, val := rng.IntN(4), byte(rng.IntN(9)+1)
				c.no(m.Write(v, off, val))
				if id := grp[v]; grpSize(grp, alive, id) > 1 {
					mem = append(mem, append([]byte(nil), mem[id]...))
					grp[v] = len(mem) - 1
				}
				mem[grp[v]][off] = val
			case 2:
				if len(alive) > 1 {
					c.no(m.Release(v))
					delete(grp, v)
					alive = without(alive, v)
				}
			}
			for _, w := range alive {
				for off := range 4 {
					g, _ := m.Read(w, off)
					c.ok(g == mem[grp[w]][off])
				}
			}
			c.no(m.Validate())
		}
	}
}

// TestRefCountConservation：表驱动多档 快照/写/释放，守恒且最终无泄漏（不变量1）。
func TestRefCountConservation(t *testing.T) {
	c := C{t}
	for _, tc := range []struct{ s, w, r int }{{0, 0, 0}, {1, 0, 1}, {3, 1, 0}, {5, 3, 4}, {10, 10, 0}} {
		m := NewManager(2, 100)
		v, _ := m.Alloc([]byte{0, 0})
		vs := []View{v}
		for i := 0; i < tc.s; i++ {
			nv, _ := m.Snapshot(v)
			vs = append(vs, nv)
		}
		for i := 0; i < tc.w; i++ {
			c.no(m.Write(vs[i], 0, byte(i+1)))
		}
		for i := 0; i < tc.r; i++ {
			c.no(m.Release(vs[0]))
			vs = vs[1:]
		}
		c.no(m.Validate())
		for _, x := range vs {
			c.no(m.Release(x))
		}
		c.ok(m.PageCount() == 0)
	}
}

// TestWriteIsolation：共享页写只改自己，其他共享者看到旧内容（不变量3）。
func TestWriteIsolation(t *testing.T) {
	c := C{t}
	m := NewManager(3, 10)
	a, _ := m.Alloc([]byte{1, 2, 3})
	b, _ := m.Snapshot(a)
	d, _ := m.Snapshot(a)
	c.no(m.Write(b, 1, 9))
	for _, x := range []struct {
		v View
		d [3]byte
	}{{a, [3]byte{1, 2, 3}}, {d, [3]byte{1, 2, 3}}, {b, [3]byte{1, 9, 3}}} {
		for i := range x.d {
			g, _ := m.Read(x.v, i)
			c.ok(g == x.d[i])
		}
	}
}

// TestWriteCheckCountConstant：直读非导出计数器，多档 m 下检查条数恒为 1（O(1)）。
func TestWriteCheckCountConstant(t *testing.T) {
	c := C{t}
	for _, n := range []int{100, 1000, 10000} {
		m := NewManager(1, n+1)
		v, _ := m.Alloc([]byte{0})
		vs := make([]View, n)
		for i := range vs {
			vs[i], _ = m.Snapshot(v)
		}
		c.no(m.Write(v, 0, 1))
		c.ok(m.lastWriteChecks == 1)
		for _, u := range vs {
			c.no(m.Release(u))
		}
		c.no(m.Release(v))
		c.ok(m.PageCount() == 0)
	}
}
