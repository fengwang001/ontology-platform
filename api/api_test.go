package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"testing"

	"ontology/api"
)

// TestErrorsDistinctNoTrace 四类哨兵互不相同；补齐后 Close 得 73 证明失败不留痕。
func TestErrorsDistinctNoTrace(t *testing.T) {
	a := api.New()
	a.Append(api.Event{"u", 1, 7})
	errs := []error{
		a.Append(api.Event{"", 1, 1}),
		a.Append(api.Event{"u", -1, 1}),
		a.Append(api.Event{"u", 2, 99}),
		a.Append(api.Event{"u", 1, 8}),
		a.Close("u", 2),
	}
	wants := []error{api.ErrBadKey, api.ErrInvalid, api.ErrInvalid, api.ErrConflict, api.ErrIncomplete}
	kind := map[error]bool{}
	for i, want := range wants {
		if !errors.Is(errs[i], want) {
			t.Fatalf("case%d: %v want %v", i, errs[i], want)
		}
		kind[want] = true
	}
	if len(kind) != 4 {
		t.Fatalf("四类哨兵应互异，实际 %d 类", len(kind))
	}
	if a.Append(api.Event{"u", 2, 3}) != nil || a.Close("u", 2) != nil {
		t.Fatal("被拒后会话不可继续使用")
	}
	if v, ok, _ := a.Result("u"); !ok || v != 73 {
		t.Fatalf("被拒留痕: u=(%d,%v) want 73", v, ok)
	}
	if a.Append(api.Event{"u", 3, 0}) != api.ErrClosed || a.Close("u", 2) != nil || a.Close("u", 3) != api.ErrClosed {
		t.Fatal("关闭语义错：Append 须拒、同 N 幂等、异 N 须拒")
	}
}

// TestBatchConsistency 随机交错多会话；关闭结果必须等于按 Seq 升序的朴素批量重算。
func TestBatchConsistency(t *testing.T) {
	for _, seed := range []int64{1, 42} {
		r, a := rand.New(rand.NewSource(seed)), api.New()
		ref := map[string]map[int]int{} // sid -> seq -> value 的朴素模型
		for i := 0; i < 200; i++ {
			sid := fmt.Sprintf("s%d", r.Intn(4))
			seq, val := 1+r.Intn(6), r.Intn(10)
			err := a.Append(api.Event{sid, seq, val})
			m := ref[sid]
			if m == nil {
				m = map[int]int{}
				ref[sid] = m
			}
			if old, dup := m[seq]; dup {
				if (old == val) != (err == nil) || (old != val && !errors.Is(err, api.ErrConflict)) {
					t.Fatalf("seed=%d 重复判定错 old=%d val=%d err=%v", seed, old, val, err)
				}
			} else if err != nil {
				t.Fatalf("seed=%d 新事件被拒: %v", seed, err)
			} else {
				m[seq] = val
			}
		}
		for sid, m := range ref {
			max, want := 0, 0
			for seq := range m {
				if seq > max {
					max = seq
				}
			}
			for seq := 1; seq <= max; seq++ { // 补齐缺口后 Close，逐位对照
				if _, ok := m[seq]; !ok {
					if err := a.Append(api.Event{sid, seq, seq % 10}); err != nil {
						t.Fatal(err)
					}
					m[seq] = seq % 10
				}
				want = want*10 + m[seq]
			}
			if a.Close(sid, max) != nil {
				t.Fatalf("seed=%d %s 补齐后 Close 应成功", seed, sid)
			}
			if v, ok, _ := a.Result(sid); !ok || v != want {
				t.Fatalf("seed=%d %s (%d)≠批量 %d", seed, sid, v, want)
			}
		}
	}
}

// TestConcurrentAppend 并发乱序 Append；Close 成功结果正确，并发读者读到的冻结值不漂移。无 sleep。
func TestConcurrentAppend(t *testing.T) {
	wantFor := map[int]int{1: 1, 5: 12345, 9: 123456789, 12: 123456789012}
	for _, n := range []int{1, 5, 9, 12} {
		a := api.New()
		var app, rd sync.WaitGroup
		for _, p := range rand.Perm(n) {
			seq := p + 1
			app.Add(1)
			go func() {
				defer app.Done()
				if err := a.Append(api.Event{"k", seq, seq % 10}); err != nil {
					t.Errorf("seq=%d: %v", seq, err)
				}
			}()
		}
		for g := 0; g < 4; g++ {
			rd.Add(1)
			go func() {
				defer rd.Done()
				saw, reads := -1, 0 // 每个读者自证：多次读到的已关闭结果必须恒定
				for reads < 64 {
					v, ok, err := a.Result("k")
					if err != nil || !ok {
						runtime.Gosched() // 关闭前让出调度；不用 sleep
						continue
					}
					if saw < 0 {
						saw = v
					} else if saw != v {
						t.Errorf("冻结值漂移: %d -> %d", saw, v)
					}
					reads++
				}
			}()
		}
		app.Wait()
		if err := a.Close("k", n); err != nil {
			t.Fatalf("n=%d Close: %v", n, err)
		}
		rd.Wait()
		if v, ok, _ := a.Result("k"); !ok || v != wantFor[n] {
			t.Fatalf("n=%d 结果=%d(ok=%v) want %d", n, v, ok, wantFor[n])
		}
	}
}

// TestSelfCheck 内置自检必须通过。
func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
