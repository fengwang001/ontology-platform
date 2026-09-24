package chunk

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/wal"
)

func TestSectionThree(t *testing.T) {
	lg, c := wal.New(), New(nil, 100)
	c.lg = lg
	lg.Append([]wal.Entry{U(10, "a"), U(15, "b"), U(20, "x")}) // L=3
	me(t, c.BeginChunk(10, 20))
	lg.Append([]wal.Entry{U(15, "c"), D(10)})
	me(t, c.ReadChunk())
	lg.Append([]wal.Entry{U(12, "d"), U(15, "e"), U(20, "y")}) // H=8
	end, e := c.EndChunk()
	me(t, e)
	if fmt.Sprint(end) != "[{1 12 d} {1 15 e}]" {
		t.Fatalf("end=%v", end)
	}
	lg.Append([]wal.Entry{U(10, "f"), D(12), U(19, "g")})
	pol, e := c.Poll()
	me(t, e)
	if fmt.Sprint(pol) != "[{1 10 f} {2 12 } {1 19 g}]" {
		t.Fatalf("poll=%v", pol)
	}
	want := map[int64]string{10: "f", 15: "e", 19: "g"} // 不含边界键 20
	if !reflect.DeepEqual(want, c.View()) {
		t.Fatalf("view=%v", c.View())
	}
}
func TestErrorsNoTrace(t *testing.T) {
	c := New(wal.New(), 100)
	end := func() error { _, e := c.EndChunk(); return e }
	cases := []struct {
		n string
		f func() error
		w error
	}{
		{"range", func() error { return c.BeginChunk(5, 5) }, ErrInvalidRange},
		{"read-stage", c.ReadChunk, ErrStage},
		{"begin", func() error { return c.BeginChunk(10, 20) }, nil},
		{"begin-stage", func() error { return c.BeginChunk(30, 40) }, ErrStage},
		{"overlap-active", func() error { return c.BeginChunk(19, 25) }, ErrOverlapping},
		{"end-stage", end, ErrStage},
		{"read", c.ReadChunk, nil},
		{"end", end, nil},
		{"overlap-done", func() error { return c.BeginChunk(19, 25) }, ErrOverlapping},
	}
	for _, x := range cases { // 按状态机时间线顺序执行
		if e := x.f(); e != x.w {
			t.Fatalf("%s: %v != %v", x.n, e, x.w)
		}
	}
	u := limitAPI(t, true) // EndChunk 超限：被拒、阶段停在已读、视图仍空
	if _, e := u.EndChunk(); e != ErrViewTooLarge {
		t.Fatalf("end overflow %v", e)
	}
	if e := u.ReadChunk(); e != ErrStage {
		t.Fatalf("stage not kept %v", e)
	}
	v := limitAPI(t, false) // Poll 超限：不推进位置，重试仍同样失败，视图不变
	_, _ = v.EndChunk()
	v.lg.Append([]wal.Entry{U(2, "b")})
	before := v.View()
	if _, e := v.Poll(); e != ErrViewTooLarge {
		t.Fatalf("poll overflow %v", e)
	}
	if _, e := v.Poll(); e != ErrViewTooLarge || len(u.View()) != 0 ||
		!reflect.DeepEqual(before, v.View()) {
		t.Fatal("rejected op left a trace")
	}
}

func TestCorrectionCheckCount(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		lg := wal.New()
		es := make([]wal.Entry, m) // 键一半在 [0,m/2) 内、一半在外
		for i := range es {
			es[i] = U(int64(i), "x")
		}
		lg.Append(es)
		c := New(lg, 1<<30)
		me(t, c.BeginChunk(0, int64(m/2)))
		lg.Append([]wal.Entry{U(1, "y"), D(2)})
		me(t, c.ReadChunk())
		lg.Append([]wal.Entry{U(3, "z")})
		_, _ = c.EndChunk()
		if c.lastChecked > 3+2 { // H−L=3，不随 m 线性增长
			t.Fatalf("m=%d checked=%d", m, c.lastChecked)
		}
	}
}

func TestConcurrent(t *testing.T) {
	lg, c := wal.New(), New(nil, 1<<30)
	c.lg = lg
	stop, wg := make(chan struct{}), sync.WaitGroup{}
	wg.Add(1)
	go func() { // 持续随机写，覆盖 10 个 chunk 的键范围与范围外；不使用 sleep
		defer wg.Done()
		rng := rand.New(rand.NewSource(99))
		for {
			select {
			case <-stop:
				return
			default:
				k := int64(rng.Intn(1200))
				if rng.Intn(3) == 0 {
					lg.Append([]wal.Entry{D(k)})
				} else {
					lg.Append([]wal.Entry{U(k, "v")})
				}
			}
		}
	}()
	var all []Out
	for ch := 0; ch < 10; ch++ {
		lo := int64(ch * 100)
		me(t, c.BeginChunk(lo, lo+100))
		me(t, c.ReadChunk())
		os, e := c.EndChunk()
		me(t, e)
		all = append(all, os...)
		os, _ = c.Poll()
		all = append(all, os...)
	}
	close(stop)
	wg.Wait()
	os, _ := c.Poll()
	all = append(all, os...)
	if !reflect.DeepEqual(lg.Snapshot(0, 1000), c.View()) { // 不变量1
		t.Fatal("concurrent view mismatch")
	}
	m := map[int64]bool{}
	for _, o := range all { // 不变量3 回放安全：每条删除时键必存在
		if o.Op == wal.Upsert {
			m[o.Key] = true
		} else if !m[o.Key] {
			t.Fatalf("delete missing %d", o.Key)
		} else {
			delete(m, o.Key)
		}
	}
}
