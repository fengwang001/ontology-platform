package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/api"
)

// 不变量 1：多档规模、随机写读顺序，Read 逐键等于批量重算。
func TestBatchEquivalence(t *testing.T) {
	keys := []string{"a", "b", "c", "d"}
	type op struct {
		del  bool
		k, v string
	}
	// batch 是不变量 1 的参照：从空按 Seq 升序应用前 atSeq 条日志。
	batch := func(ops []op, atSeq int, key string) (v string, ok bool) {
		for i := 0; i < atSeq && i < len(ops); i++ {
			if ops[i].k == key {
				if ops[i].del {
					v, ok = "", false
				} else {
					v, ok = ops[i].v, true
				}
			}
		}
		return
	}
	for _, n := range []int{10, 100, 1000} {
		rng := rand.New(rand.NewSource(int64(n)))
		s := api.New()
		var ops []op
		for i := 0; i < n; i++ {
			o := op{del: rng.Intn(3) == 0, k: keys[rng.Intn(len(keys))], v: fmt.Sprintf("v%d", i)}
			if o.del {
				s.Del(o.k)
			} else {
				s.Put(o.k, o.v)
			}
			ops = append(ops, o)
			if rng.Intn(7) == 0 {
				s.Snapshot()
			}
		}
		for at := 1; at <= len(ops); at++ {
			for _, k := range keys {
				got, ok, err := s.Read(k, int64(at))
				if w, wok := batch(ops, at, k); err == nil && (got != w || ok != wok) {
					t.Fatalf("n=%d Read(%q,%d)=(%q,%v,%v) want (%q,%v)", n, k, at, got, ok, err, w, wok)
				}
			}
		}
	}
}

// 不变量 2：同键交叠时增量 Seq 最大者胜，快照绝不覆盖增量。
func TestOverlapPriority(t *testing.T) {
	s := api.New()
	s.Put("a", "1")
	s.Put("b", "2")
	s.Snapshot()
	s.Put("a", "5")
	s.Del("b")
	// want 空串表示不存在；增量覆盖/快照回退/增量删除三类
	cases := []struct {
		k, want string
		at      int64
	}{
		{"a", "5", 3}, {"a", "1", 2}, {"b", "", 4},
	}
	for _, c := range cases {
		got, ok, _ := s.Read(c.k, c.at)
		if got != c.want || ok != (c.want != "") {
			t.Fatalf("Read(%q,%d)=(%q,%v) want (%q,%v)", c.k, c.at, got, ok, c.want, c.want != "")
		}
	}
}

// 不变量 3：Snapshot 不改变任何 Read 结果。
func TestSnapshotInvariance(t *testing.T) {
	s := api.New()
	s.Put("x", "1")
	s.Put("y", "2")
	s.Del("y")
	rd := func() string {
		v1, ok1, _ := s.Read("x", 3)
		v2, ok2, _ := s.Read("y", 3)
		return fmt.Sprintf("%q:%v:%q:%v", v1, ok1, v2, ok2)
	}
	before := rd()
	s.Snapshot()
	if rd() != before {
		t.Fatal("快照改变读结果")
	}
}

// 不变量 4：三类错误可判定互不相同；被拒后状态不变、仍可继续使用。
func TestFailureNoTrace(t *testing.T) {
	s := api.New()
	s.Put("a", "1")
	s.Snapshot()
	s.Put("a", "2")
	e1, e2, e3 := s.Put("", "x"), s.Del(""), s.Put("k", "")
	if !errors.Is(e1, api.ErrEmptyKey) || !errors.Is(e2, api.ErrEmptyKey) || !errors.Is(e3, api.ErrEmptyVal) ||
		api.ErrEmptyKey == api.ErrEmptyVal || api.ErrEmptyVal == api.ErrBeforeSnap {
		t.Fatal("三类哨兵错误不匹配或不可区分")
	}
	if _, _, err := s.Read("a", 0); !errors.Is(err, api.ErrBeforeSnap) {
		t.Fatal("atSeq<snapSeq 未拒绝")
	}
	if v, ok, _ := s.Read("a", 2); v != "2" || !ok {
		t.Fatal("被拒操作改变了状态")
	}
	if err := s.Put("b", "3"); err != nil {
		t.Fatal("被拒后不可继续使用")
	}
}

// 并发：N 个 goroutine 读同一 atSeq 结果逐字段相同，期间反复 Snapshot+SelfCheck。
func TestConcurrentRead(t *testing.T) {
	s := api.New()
	for i := 0; i < 50; i++ {
		s.Put(fmt.Sprintf("k%d", i), "v")
	}
	s.Snapshot()
	s.Put("k0", "v2")
	got := make([]string, 16)
	var wg sync.WaitGroup
	start := make(chan struct{})
	spawn := func(f func()) { wg.Add(1); go func() { defer wg.Done(); <-start; f() }() }
	for i := range got {
		spawn(func() { v, ok, err := s.Read("k0", 51); got[i] = fmt.Sprintf("%q:%v:%v", v, ok, err) })
	}
	spawn(func() {
		for i := 0; i < 100; i++ {
			s.Snapshot()
			s.SelfCheck()
		}
	})
	close(start)
	wg.Wait()
	if strings.Join(got, "") != strings.Repeat(`"v2":true:<nil>`, 16) {
		t.Fatal("读到中间态")
	}
}
