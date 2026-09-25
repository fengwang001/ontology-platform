package s

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
)

// refStack 是 sync.Mutex 保护的朴素切片栈，作为一致性参照（I1）。
type refStack struct {
	mu sync.Mutex
	vs []int
}

func (r *refStack) push(v int) { r.mu.Lock(); r.vs = append(r.vs, v); r.mu.Unlock() }
func (r *refStack) pop() (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.vs) == 0 {
		return 0, false
	}
	v := r.vs[len(r.vs)-1]
	r.vs = r.vs[:len(r.vs)-1]
	return v, true
}
func (r *refStack) len() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.vs) }

// SelfCheck 在内部新建栈跑内置序列，核验四条不变量；全过返回 nil。
// 不触碰接收者状态，可被多个 goroutine 并发调用。
func (s *Stack) SelfCheck() error {
	// I1/I2：多组随机交错序列，与朴素栈逐次比对 Pop 返回值与 Len。
	for _, seed := range []int64{1, 7, 42, 99, 2026} {
		st, _ := New(7)
		ref := &refStack{}
		rng := rand.New(rand.NewSource(seed))
		for i := 0; i < 3000; i++ {
			if rng.Intn(10) < 6 {
				v := rng.Intn(1_000_000)
				err := st.Push(v)
				if ref.len() < 7 {
					ref.push(v)
				} else if err != ErrFull {
					return fmt.Errorf("I1 seed %d: full push mismatch", seed)
				}
			} else {
				v, ok := st.Pop()
				rv, rok := ref.pop()
				if v != rv || ok != rok {
					return fmt.Errorf("I1 seed %d: pop %d,%v vs ref %d,%v", seed, v, ok, rv, rok)
				}
			}
			if st.Len() != ref.len() || st.Len() < 0 || st.Len() > 7 {
				return fmt.Errorf("I1/I2 seed %d: Len diverged %d vs %d", seed, st.Len(), ref.len())
			}
		}
	}
	// I3：1000 个唯一值全部弹空，每个恰好出现一次。
	st, _ := New(1000)
	seen := make(map[int]int, 1000)
	for i := 1; i <= 1000; i++ {
		if err := st.Push(i); err != nil {
			return err
		}
	}
	for i := 0; i < 1000; i++ {
		v, ok := st.Pop()
		if !ok {
			return fmt.Errorf("I3: early empty at %d", i)
		}
		seen[v]++
	}
	if len(seen) != 1000 {
		return fmt.Errorf("I3: got %d distinct values, want 1000", len(seen))
	}
	for v, c := range seen {
		if c != 1 {
			return fmt.Errorf("I3: value %d appeared %d times", v, c)
		}
	}
	if _, ok := st.Pop(); ok || st.Len() != 0 {
		return errors.New("I3: stack not empty after draining")
	}
	// I4：非法参数/满/已关闭三类拒绝均不改状态。
	if _, err := New(0); err != ErrInvalidMaxLen {
		return errors.New("I4: New(0) not rejected")
	}
	full, _ := New(1)
	if err := full.Push(1); err != nil {
		return err
	}
	if err := full.Push(2); err != ErrFull || full.Len() != 1 {
		return errors.New("I4: full push traced")
	}
	if err := full.Close(); err != nil {
		return err
	}
	if err := full.Push(3); err != ErrClosed || full.Len() != 1 {
		return errors.New("I4: push after close traced")
	}
	return nil
}
