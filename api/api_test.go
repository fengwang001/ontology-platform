package api

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

type cell struct {
	v int64
	d bool // 已删除
}

// 与朴素参照逐 key 核验。postCompact 时「已删除」允许因孤独墓碑被丢弃而表现为
// 「不存在」（Compact 规则所致），但绝不许复活成值。
func checkAll(t *testing.T, s *Store, m map[string]cell, keys []string, postCompact bool) {
	t.Helper()
	for _, k := range keys {
		v, ok, del, err := s.Get(k)
		if err != nil {
			t.Fatal(err)
		}
		c, seen := m[k]
		switch {
		case !seen && (ok || del):
			t.Fatalf("%s: got (%d,%v,%v), want not-exist", k, v, ok, del)
		case seen && c.d && ok:
			t.Fatalf("%s: deleted key resurrected as %d", k, v)
		case seen && c.d && !del && !postCompact:
			t.Fatalf("%s: got not-exist, want deleted", k)
		case seen && !c.d && (!ok || del || v != c.v):
			t.Fatalf("%s: got (%d,%v,%v), want %d", k, v, ok, del, c.v)
		}
	}
}

// 不变量1：任意写序列后与朴素参照逐 key 一致（含已删除/不存在的区别）。
func TestNaiveConsistency(t *testing.T) {
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h", "never"}
	for _, cfg := range []struct{ maxMem, ops int }{{1, 300}, {2, 500}, {7, 800}, {64, 1000}} {
		t.Run(fmt.Sprintf("maxMem=%d", cfg.maxMem), func(t *testing.T) {
			s, err := New(cfg.maxMem)
			if err != nil {
				t.Fatal(err)
			}
			m := map[string]cell{}
			st := uint64(cfg.maxMem*2654435761 + 7)
			next := func(n uint64) uint64 {
				st = st*6364136223846793005 + 1442695040888963407
				return (st >> 33) % n
			}
			for i := 0; i < cfg.ops; i++ {
				k := keys[next(8)] // "never" 永不写入，核验不存在
				if next(4) == 0 {
					m[k] = cell{d: true}
					if err := s.Del(k); err != nil {
						t.Fatal(err)
					}
				} else {
					v := int64(next(100000))
					m[k] = cell{v: v}
					if err := s.Put(k, v); err != nil {
						t.Fatal(err)
					}
				}
			}
			checkAll(t, s, m, keys, false)
			s.Compact() // 不变量3：合并不复活已删除的 key
			checkAll(t, s, m, keys, true)
		})
	}
}

// 不变量4：被拒操作不改变任何状态，三类错误互不相同，之后仍可正常使用。
func TestRejectedOpsNoSideEffect(t *testing.T) {
	for _, bad := range []int{0, -5} {
		if _, err := New(bad); !errors.Is(err, ErrBadMaxMem) {
			t.Fatalf("New(%d)=%v, want ErrBadMaxMem", bad, err)
		}
	}
	s, _ := New(2)
	s.Put("a", 1)
	s.Put("b", 2)
	s.Put("c", 3) // 冻结出一个 SSTable
	_, _, _, _ = s.Get("miss")
	amp := s.ReadAmp()
	long := string(make([]byte, 65))
	for _, err := range []error{s.Put("", 1), s.Del("")} {
		if !errors.Is(err, ErrEmptyKey) {
			t.Fatalf("empty key err=%v", err)
		}
	}
	for _, err := range []error{s.Put(long, 1), s.Del(long)} {
		if !errors.Is(err, ErrKeyTooLong) {
			t.Fatalf("long key err=%v", err)
		}
	}
	if _, _, _, err := s.Get(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Get empty err=%v", err)
	}
	if errors.Is(ErrEmptyKey, ErrKeyTooLong) || errors.Is(ErrKeyTooLong, ErrBadMaxMem) {
		t.Fatal("sentinel errors must be distinct")
	}
	if s.ReadAmp() != amp {
		t.Fatalf("ReadAmp changed: %d -> %d", amp, s.ReadAmp())
	}
	if v, ok, _, _ := s.Get("c"); !ok || v != 3 {
		t.Fatalf("Get(c)=%d,%v, want 3,true", v, ok)
	}
	if err := s.Put("d", 4); err != nil {
		t.Fatalf("store unusable after rejects: %v", err)
	}
}

// 并发：N 个 goroutine 并发 Put 不同 key，全部可 Get；并发读已写 key 值一致。
func TestConcurrentPutGet(t *testing.T) {
	s, _ := New(8)
	s.Put("const", 42)
	const n = 128
	var wg sync.WaitGroup
	bad := make(chan string, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			if err := s.Put(fmt.Sprintf("k%04d", g), int64(g)); err != nil {
				bad <- fmt.Sprintf("k%04d", g)
			}
			if v, ok, _, _ := s.Get("const"); ok && v != 42 {
				bad <- "const"
			}
			s.ReadAmp()
			s.Compact()
		}(g)
	}
	wg.Wait()
	close(bad)
	for k := range bad {
		t.Fatalf("concurrent op failed on %s", k)
	}
	for g := 0; g < n; g++ {
		if v, ok, _, _ := s.Get(fmt.Sprintf("k%04d", g)); !ok || v != int64(g) {
			t.Fatalf("k%04d: got %d,%v", g, v, ok)
		}
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
