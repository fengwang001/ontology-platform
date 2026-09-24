package api

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"sync"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestReferenceEquivalence 不变量 1：随机序列下 Recover 恒等于朴素参照
// （每次 Checkpoint 深拷贝整张活跃 map，取最后一次），并对拍 View。
func TestReferenceEquivalence(t *testing.T) {
	const maxKeys = 12
	for seed := int64(0); seed < 20; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			s, naive, saved := New(maxKeys), map[string]int64{}, map[string]int64{}
			must(t, s.Checkpoint()) // 保证至少一个基线
			for range 200 {
				k := fmt.Sprintf("k%d", rng.Intn(10))
				switch rng.Intn(6) {
				case 0, 1, 2, 3: // Set：随机到达顺序，含 0/负值
					v := int64(rng.Intn(5) - 2)
					_, existed := naive[k]
					err := s.Set(k, v)
					if !existed && len(naive) >= maxKeys {
						if !errors.Is(err, ErrTooManyKeys) {
							t.Fatalf("want ErrTooManyKeys, got %v", err)
						}
						continue
					}
					must(t, err)
					naive[k] = v
				case 4: // Delete
					s.Delete(k)
					delete(naive, k)
				default: // Checkpoint：朴素参照直接深拷贝
					must(t, s.Checkpoint())
					saved = maps.Clone(naive)
				}
			}
			rec, err := s.Recover()
			if err != nil || !maps.Equal(rec, saved) || !maps.Equal(s.View(), naive) {
				t.Fatalf("recover=%v err=%v want=%v; view-ok=%v", rec, err, saved, maps.Equal(s.View(), naive))
			}
		})
	}
}

// TestRejectedOpsLeaveNoTrace 不变量 4：三类错误可判定且互不相同，被拒后状态不变、仍可用。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	cases := []struct {
		name string
		call func(*Store) error
		want error
	}{
		{"empty key", func(s *Store) error { return s.Set("", 9) }, ErrEmptyKey},
		{"over limit on new key", func(s *Store) error { return s.Set("b", 2) }, ErrTooManyKeys},
		{"recover without base", func(s *Store) error { _, e := s.Recover(); return e }, ErrNoBase},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(1)
			must(t, s.Set("a", 1))
			if err := tc.call(s); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !maps.Equal(s.View(), map[string]int64{"a": 1}) { // 失败不留痕
				t.Fatalf("state changed after rejection: %v", s.View())
			}
			if err := s.Checkpoint(); err != nil { // 仍可继续正常使用
				t.Fatalf("unusable after rejection: %v", err)
			}
			rec, err := s.Recover()
			if err != nil || !maps.Equal(rec, map[string]int64{"a": 1}) {
				t.Fatalf("recover after rejection = %v err=%v", rec, err)
			}
		})
	}
	if ErrEmptyKey == ErrTooManyKeys || ErrTooManyKeys == ErrNoBase || ErrEmptyKey == ErrNoBase {
		t.Fatal("the three sentinel errors must be pairwise distinct")
	}
}

// TestMaxKeysBoundary 恰好放满允许、再新增被拒、覆盖既有键始终允许。
func TestMaxKeysBoundary(t *testing.T) {
	s := New(2)
	must(t, s.Set("a", 1))
	must(t, s.Set("b", 2))
	if err := s.Set("c", 3); !errors.Is(err, ErrTooManyKeys) {
		t.Fatalf("3rd new key: err = %v, want ErrTooManyKeys", err)
	}
	if err := s.Set("a", 10); err != nil { // 覆盖不占新名额
		t.Fatalf("overwriting existing key should be allowed: %v", err)
	}
	if !maps.Equal(s.View(), map[string]int64{"a": 10, "b": 2}) {
		t.Fatalf("view = %v", s.View())
	}
}

// TestConcurrentReadOnly N 个 goroutine 并发只读同一已检查点实例；无 sleep，-race 必须干净。
func TestConcurrentReadOnly(t *testing.T) {
	s := New(16)
	want := map[string]int64{}
	for i := 0; i < 8; i++ {
		k := fmt.Sprintf("k%d", i)
		must(t, s.Set(k, int64(i)))
		want[k] = int64(i)
	}
	must(t, s.Checkpoint())
	const N = 32
	var wg sync.WaitGroup
	outs, selfErrs := make([][2]map[string]int64, N), make(chan error, N)
	for g := range N {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r, err := s.Recover()
			if err != nil {
				selfErrs <- err
				return
			}
			outs[g] = [2]map[string]int64{r, s.View()}
			selfErrs <- s.SelfCheck()
		}(g)
	}
	wg.Wait()
	close(selfErrs)
	for err := range selfErrs {
		if err != nil {
			t.Fatalf("concurrent reader/selfcheck: %v", err)
		}
	}
	for g := range N {
		if !maps.Equal(outs[g][0], want) || !maps.Equal(outs[g][1], want) {
			t.Fatalf("reader %d got %v / %v, want %v", g, outs[g][0], outs[g][1], want)
		}
	}
}
