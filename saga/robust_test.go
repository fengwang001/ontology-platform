package saga_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/journal"
	"ontology/saga"
)

// 四类拒绝的优先级：参数非法 > 实例 > 状态不符 > 步骤不符；被拒操作不落日志、不登记。
func TestErrorPriority(t *testing.T) {
	s, jr, eff := newSaga()
	id := []byte("e1")
	for _, c := range []struct{ n, p, b int }{
		{0, 0, 0}, {65, 0, 0}, {3, 3, 0}, {3, -1, 0}, {3, 1, 11}, {3, 1, -1},
	} {
		if err := s.Begin(id, c.n, c.p, c.b); !errors.Is(err, saga.ErrInvalidArg) {
			t.Errorf("Begin(n=%d,p=%d,B=%d) 应报参数非法，实际 %v", c.n, c.p, c.b, err)
		}
	}
	if err := s.Begin(nil, 3, 1, 1); !errors.Is(err, saga.ErrInvalidArg) {
		t.Errorf("空 id 应报参数非法，实际 %v", err)
	}
	if err := s.Intent(id, 0); !errors.Is(err, saga.ErrInstance) {
		t.Errorf("未 Begin 应报实例不存在，实际 %v", err)
	}
	if err := s.Intent(id, 64); !errors.Is(err, saga.ErrInvalidArg) {
		t.Errorf("下标越界应优先于实例不存在，实际 %v", err)
	}
	must(t, s.Begin(id, 3, 1, 1))
	if err := s.Begin(id, 3, 1, 1); !errors.Is(err, saga.ErrInstance) {
		t.Errorf("重复 Begin 应报实例已存在，实际 %v", err)
	}
	if err := s.Intent(id, 3); !errors.Is(err, saga.ErrInvalidArg) {
		t.Errorf("下标 3≥n=3 应报参数非法，实际 %v", err)
	}
	if err := s.CompIntent(id, 0); !errors.Is(err, saga.ErrState) {
		t.Errorf("计划前滚时 CompIntent 应报状态不符，实际 %v", err)
	}
	if err := s.Probe(id, true); !errors.Is(err, saga.ErrState) {
		t.Errorf("计划前滚时 Probe 应报状态不符，实际 %v", err)
	}
	if err := s.Done(id, 0); !errors.Is(err, saga.ErrState) {
		t.Errorf("无在途时 Done 应报状态不符，实际 %v", err)
	}
	if err := s.Intent(id, 1); !errors.Is(err, saga.ErrStep) {
		t.Errorf("计划前滚 0 时 Intent(1) 应报步骤不符，实际 %v", err)
	}
	if err := s.Fail(id, 0, journal.FailKind(9)); !errors.Is(err, saga.ErrInvalidArg) {
		t.Errorf("非法失败类别应报参数非法，实际 %v", err)
	}
	if jr.Len("e1") != 1 {
		t.Fatalf("被拒操作不追加记录，日志应只有 B，实际 %d 条", jr.Len("e1"))
	}
	if a, d := eff.Totals(); a != 0 || d != 0 {
		t.Fatalf("被拒操作不登记副作用，实际新增 %d、重复 %d", a, d)
	}
	t.Logf("判定依据: 优先级 参数非法>实例>状态不符>步骤不符；被拒后日志仍为 [B]，副作用为空")

	// 在途时只允许配对动作；Recover 放弃在途后，迟到 Done 报状态不符。
	must(t, s.Intent(id, 0))
	if err := s.Intent(id, 0); !errors.Is(err, saga.ErrState) {
		t.Errorf("在途时再 Intent 应报状态不符，实际 %v", err)
	}
	if err := s.CompDone(id, 0); !errors.Is(err, saga.ErrState) {
		t.Errorf("I 在途时 CompDone 应报状态不符，实际 %v", err)
	}
	if err := s.Done(id, 1); !errors.Is(err, saga.ErrState) {
		t.Errorf("在途步骤不配对应报状态不符，实际 %v", err)
	}
	if _, err := s.Recover(id); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if err := s.Done(id, 0); !errors.Is(err, saga.ErrState) {
		t.Errorf("Recover 放弃在途后，迟到 Done 应报状态不符，实际 %v", err)
	}
	t.Logf("判定依据: 在途只允许配对的 Done/Fail/CompDone；Recover 不写日志、清除在途位")
}

// 不同实例并发推进、同一实例并发 Recover：-race 下应无数据竞争。
func TestConcurrent(t *testing.T) {
	s, _, _ := newSaga()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := []byte(fmt.Sprintf("inst-%d", g))
			if err := s.Begin(id, 4, 1, 1); err != nil {
				t.Errorf("Begin: %v", err)
				return
			}
			failed := map[int]bool{}
			for {
				plan, err := s.Recover(id)
				if err != nil {
					t.Errorf("Recover: %v", err)
					return
				}
				switch plan.Kind {
				case saga.Forward:
					if err := s.Intent(id, plan.Step); err != nil {
						t.Errorf("Intent: %v", err)
						return
					}
					if !failed[plan.Step] && (g+plan.Step)%3 == 0 {
						failed[plan.Step] = true
						if err := s.Fail(id, plan.Step, journal.Transient); err != nil {
							t.Errorf("Fail: %v", err)
							return
						}
						continue
					}
					if err := s.Done(id, plan.Step); err != nil {
						t.Errorf("Done: %v", err)
						return
					}
				case saga.Compensate:
					if err := s.CompIntent(id, plan.Step); err != nil {
						t.Errorf("CompIntent: %v", err)
						return
					}
					if err := s.CompDone(id, plan.Step); err != nil {
						t.Errorf("CompDone: %v", err)
						return
					}
				case saga.ProbePivot:
					if err := s.Probe(id, true); err != nil {
						t.Errorf("Probe: %v", err)
						return
					}
				default:
					return // 终态
				}
			}
		}(g)
	}
	// 同一实例并发 Recover（只读，应安全且结果一致）。
	shared := []byte("shared")
	must(t, s.Begin(shared, 3, 1, 1))
	must(t, s.Intent(shared, 0))
	for k := 0; k < 8; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			plan, err := s.Recover(shared)
			if err != nil || plan != (saga.Plan{Kind: saga.Compensate, Step: 0}) {
				t.Errorf("并发 Recover 得 %v, %v", plan, err)
			}
		}()
	}
	wg.Wait()
	t.Logf("判定依据: 每实例独立锁，不同实例可并发；同实例操作等价于某个串行顺序")
}
