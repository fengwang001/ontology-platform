package api

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func TestNaiveReplayRandom(t *testing.T) {
	keys := []string{"a", "b", "c", "d", "e", "f"}
	for seed := int64(0); seed < 20; seed++ {
		r := rand.New(rand.NewSource(seed))
		s, _ := New(1 + r.Intn(8))
		m := map[string]int64{} // 缺键=不存在；-1=已删除；>=0=值
		for i := 0; i < 300; i++ {
			k := keys[r.Intn(6)]
			if cur, has := m[k]; r.Intn(3) == 2 && has && cur >= 0 {
				must(t, s.Del(k))
				m[k] = -1
			} else {
				v := r.Int63n(1000)
				must(t, s.Put(k, v))
				m[k] = v
			}
		}
		for _, k := range keys {
			v, hit, del, _ := s.Get(k)
			w, has := m[k]
			if (!has && hit) || (has && w < 0 && (!hit || !del)) || (has && w >= 0 && (!hit || del || v != w)) {
				t.Fatalf("seed=%d %s %d/%v/%v model=%d", seed, k, v, hit, del, w)
			}
		}
	}
}
func TestEightSteps(t *testing.T) {
	s, _ := New(2)
	must(t, s.Put("k1", 1))
	must(t, s.Put("k2", 2))
	must(t, s.Put("k1", 3))
	must(t, s.Del("k2"))
	must(t, s.Put("k3", 4))
	if v, hit, del, _ := s.Get("k1"); !hit || del || v != 3 || s.ReadAmp() != 1 {
		t.Fatalf("step6 %d %v %v", v, hit, del)
	}
	must(t, s.Del("k1"))
	if _, hit, del, _ := s.Get("k1"); !hit || !del || s.ReadAmp() != 0 {
		t.Fatalf("step8 %v %v", hit, del)
	}
}
func TestReadOrder(t *testing.T) {
	s, _ := New(1)
	for _, p := range [][2]any{{"a", int64(1)}, {"p1", int64(0)}, {"a", int64(2)}, {"p2", int64(0)}} {
		must(t, s.Put(p[0].(string), p[1].(int64)))
	}
	if v, hit, del, _ := s.Get("a"); !hit || del || v != 2 {
		t.Fatalf("got %d %v %v", v, hit, del)
	}
}
func TestTombstoneRetention(t *testing.T) {
	s, _ := New(2)
	must(t, s.Put("k1", 1))
	must(t, s.Put("k2", 2))
	must(t, s.Put("k1", 3))
	must(t, s.Del("k2"))
	must(t, s.Put("k3", 4))
	must(t, s.Del("k1"))
	s.Compact()
	for _, c := range []struct {
		k   string
		del bool
		v   int64
	}{{"k1", true, 0}, {"k2", true, 0}, {"k3", false, 4}} {
		v, hit, del, _ := s.Get(c.k)
		if !hit || del != c.del || (!del && v != c.v) {
			t.Fatalf("%s %d %v %v", c.k, v, hit, del)
		}
	}
	q, _ := New(1)
	must(t, q.Del("z"))
	must(t, q.Put("pad", 0))
	q.Compact()
	if _, hit, _, _ := q.Get("z"); hit {
		t.Fatal("lone frozen tombstone must be dropped")
	}
}
func TestRejectedOpsNoTrace(t *testing.T) {
	if ErrEmptyKey == ErrKeyTooLong || ErrKeyTooLong == ErrInvalidMaxMem || ErrEmptyKey == ErrInvalidMaxMem {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
	if _, err := New(0); !errors.Is(err, ErrInvalidMaxMem) {
		t.Fatalf("New(0): %v", err)
	}
	s, _ := New(1)
	must(t, s.Put("x", 9))
	_, _, _, _ = s.Get("x")
	amp := s.ReadAmp()
	long := strings.Repeat("k", 65)
	if !errors.Is(s.Put("", 1), ErrEmptyKey) || !errors.Is(s.Put(long, 1), ErrKeyTooLong) || !errors.Is(s.Del(""), ErrEmptyKey) || !errors.Is(s.Del(long), ErrKeyTooLong) {
		t.Fatal("rejection sentinel mismatch")
	}
	if _, _, _, err := s.Get(""); !errors.Is(err, ErrEmptyKey) || s.seq != 1 || s.tables.TableCount() != 0 || s.ReadAmp() != amp {
		t.Fatal("rejected op changed state")
	}
	if v, hit, del, _ := s.Get("x"); !hit || del || v != 9 {
		t.Fatal("store unusable after rejection")
	}
}
func TestConcurrentPuts(t *testing.T) {
	const nW = 128
	s, _ := New(7)
	must(t, s.Put("anchor", 777))
	var wg sync.WaitGroup
	for i := 0; i < nW; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 300; j++ { // 与其他写并发：已写 anchor 的值必须始终一致
				if v, ok, del, _ := s.Get("anchor"); !ok || del || v != 777 {
					t.Error("inconsistent concurrent read")
					return
				}
			}
			if i%16 == 0 {
				s.Compact()
				if err := s.SelfCheck(); err != nil {
					t.Error(err)
					return
				}
			}
			if err := s.Put(fmt.Sprintf("key%04d", i), int64(i)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < nW; i++ {
		if v, hit, del, _ := s.Get(fmt.Sprintf("key%04d", i)); !hit || del || v != int64(i) {
			t.Fatalf("key%04d = %d %v %v", i, v, hit, del)
		}
	}
}
