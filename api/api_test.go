package api_test

import (
	"errors"
	"strconv"
	"testing"

	"ontology/api"
)

func mustNew(t *testing.T, ttl int64) *api.Store {
	t.Helper()
	s, err := api.New(ttl)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestSevenStep 钉住第三节七步序列的每步返回值与最终视图。
func TestSevenStep(t *testing.T) {
	s := mustNew(t, 10)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Set("k1", "v1", 5))
	must(s.Set("k2", "v2", 7))
	v, ok, err := s.Get("k1", 14)
	must(err)
	if !ok || v != "v1" {
		t.Fatalf("step3: got %q,%v", v, ok)
	}
	must(s.Set("k3", "v3", 15))
	if _, ok, _ := s.Get("k1", 15); ok {
		t.Fatal("step5: k1 应过期并删除")
	}
	if n, _ := s.Sweep(20); n != 1 {
		t.Fatalf("step6: 应删 1 个(k2)，实删 %d", n)
	}
	if _, ok, _ := s.Get("k3", 25); ok {
		t.Fatal("step7: k3 应过期并删除")
	}
	if view, _ := s.View(25); len(view) != 0 {
		t.Fatalf("最终视图应为空，实得 %v", view)
	}
}

// TestNaiveConsistency 钉住不变量 1：随机操作序列后 View 与朴素判定逐 Key 相同。
func TestNaiveConsistency(t *testing.T) {
	type ent struct {
		v  string
		ts int64
	}
	for _, c := range []struct{ ttl, seed int64 }{{7, 1}, {10, 2}, {3, 99}} {
		s := mustNew(t, c.ttl)
		ref := map[string]ent{}
		now := c.seed
		for i := 0; i < 300; i++ {
			now += (c.seed + int64(i)) % 5
			k := "k" + strconv.Itoa((i*7+int(c.seed))%23)
			switch i % 3 {
			case 0:
				v := "v" + strconv.Itoa(i)
				if err := s.Set(k, v, now); err != nil {
					t.Fatal(err)
				}
				ref[k] = ent{v, now}
			case 1:
				if _, _, err := s.Get(k, now); err != nil {
					t.Fatal(err)
				}
			case 2:
				if _, err := s.Sweep(now); err != nil {
					t.Fatal(err)
				}
			}
			for k, e := range ref { // 朴素判定：保留 now−Ts < ttl
				if now-e.ts >= c.ttl {
					delete(ref, k)
				}
			}
			view, err := s.View(now)
			if err != nil {
				t.Fatal(err)
			}
			if len(view) != len(ref) {
				t.Fatalf("c=%+v i=%d: |view|=%d |ref|=%d", c, i, len(view), len(ref))
			}
			for k, e := range ref {
				if view[k] != e.v {
					t.Fatalf("c=%+v i=%d key %s: view=%q ref=%q", c, i, k, view[k], e.v)
				}
			}
		}
	}
}

// TestBoundedMemory 钉住不变量 2：Sweep 后无过期残留；Get 过期即删不残留。
func TestBoundedMemory(t *testing.T) {
	s := mustNew(t, 10)
	for i := 0; i < 100; i++ { // Ts 分别为 0..99，时钟上界 99
		if err := s.Set("k"+strconv.Itoa(i), "v", int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		now      int64
		del, rem int
	}{{100, 91, 9}, {105, 5, 4}, {1000, 4, 0}} {
		n, err := s.Sweep(c.now)
		if err != nil || n != c.del {
			t.Fatalf("Sweep(%d)=(%d,%v)，应删 %d", c.now, n, err, c.del)
		}
		if view, _ := s.View(c.now); len(view) != c.rem {
			t.Fatalf("Sweep(%d) 后应剩 %d，实剩 %d", c.now, c.rem, len(view))
		}
	}
	s2 := mustNew(t, 10)
	_ = s2.Set("x", "1", 5)
	if _, ok, _ := s2.Get("x", 20); ok {
		t.Fatal("过期 key 应 ok=false")
	}
	if n, _ := s2.Sweep(20); n != 0 {
		t.Fatalf("Get 未删过期 key，Sweep 又删到 %d 个", n)
	}
}

// TestErrorsDistinct 钉住三类哨兵错误可判定且互不相同。
func TestErrorsDistinct(t *testing.T) {
	for _, bad := range []int64{0, -1, -100} {
		if _, err := api.New(bad); !errors.Is(err, api.ErrBadTTL) {
			t.Fatalf("ttl=%d 应返回 ErrBadTTL", bad)
		}
	}
	s := mustNew(t, 10)
	_ = s.Set("a", "1", 5)
	e1 := s.Set("", "x", 6)
	_, _, e2 := s.Get("a", 4)
	if !errors.Is(e1, api.ErrEmptyKey) || !errors.Is(e2, api.ErrBackwardClock) {
		t.Fatal("空 key / 时钟回退应返回对应哨兵错误")
	}
	if errors.Is(api.ErrBadTTL, api.ErrEmptyKey) || errors.Is(api.ErrEmptyKey, api.ErrBackwardClock) ||
		errors.Is(api.ErrBadTTL, api.ErrBackwardClock) {
		t.Fatal("三类哨兵错误必须互不相同")
	}
}
