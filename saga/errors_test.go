package saga_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/effect"
	"ontology/journal"
	"ontology/saga"
)

func mustErrIs(t *testing.T, err, target error, what string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: got %v, want errors.Is %v", what, err, target)
	}
}

// TestErrorPriority 覆盖四类拒绝及其优先级，且被拒不增记录。
func TestErrorPriority(t *testing.T) {
	jr, ef := journal.New(), effect.New()
	m := saga.New(jr, ef)

	// 参数非法优先于实例不存在：空 id + 越界下标。
	mustErrIs(t, m.Begin("", 3, 1, 1), saga.ErrInvalidArg, "空 id Begin")
	mustErrIs(t, m.Begin("a", 0, 0, 0), saga.ErrInvalidArg, "n=0")
	mustErrIs(t, m.Begin("a", 65, 0, 0), saga.ErrInvalidArg, "n=65")
	mustErrIs(t, m.Begin("a", 3, 3, 0), saga.ErrInvalidArg, "p≥n")
	mustErrIs(t, m.Begin("a", 3, 1, 11), saga.ErrInvalidArg, "B=11")
	mustErrIs(t, m.Intent("", 0), saga.ErrInvalidArg, "空 id Intent")
	mustErrIs(t, m.Intent("ghost", 0), saga.ErrInstance, "实例不存在")

	if err := m.Begin("a", 3, 1, 1); err != nil {
		t.Fatal(err)
	}
	mustErrIs(t, m.Begin("a", 3, 1, 1), saga.ErrInstance, "重复 Begin")
	mustErrIs(t, m.Intent("a", 3), saga.ErrInvalidArg, "下标越界")

	// 状态不符：计划为前滚 0 时 CompIntent；步骤不符：前滚 0 时 Intent(1)。
	mustErrIs(t, m.CompIntent("a", 0), saga.ErrState, "计划前滚时补偿")
	mustErrIs(t, m.Intent("a", 1), saga.ErrStep, "计划前滚 0 时 Intent(1)")
	mustErrIs(t, m.Done("a", 0), saga.ErrState, "无在途 Done")
	mustErrIs(t, m.Probe("a", true), saga.ErrState, "非探测计划 Probe")

	// 在途期间：只允许配对的 Done/Fail。
	if err := m.Intent("a", 0); err != nil {
		t.Fatal(err)
	}
	mustErrIs(t, m.Intent("a", 0), saga.ErrState, "在途期间再 Intent")
	mustErrIs(t, m.CompDone("a", 0), saga.ErrState, "I 在途时 CompDone")
	mustErrIs(t, m.Done("a", 1), saga.ErrStep, "在途 0 时 Done(1)")

	// Recover 放弃在途：迟到的 Done 报状态不符。
	if _, err := m.Recover("a"); err != nil {
		t.Fatal(err)
	}
	mustErrIs(t, m.Done("a", 0), saga.ErrState, "Recover 后迟到的 Done")

	// 全部被拒操作均未追加记录：日志仍只有 [B, I0]。
	if got := jr.Len("a"); got != 2 {
		t.Fatalf("被拒操作追加了记录: len=%d, want 2", got)
	}
	if added, _ := ef.Totals("a", effect.Fwd); added != 1 {
		t.Fatalf("被拒操作登记了副作用: fwd 新增=%d, want 1", added)
	}
	t.Logf("判定依据: 参数>实例>状态>步骤；日志=%v 未被拒绝操作污染", jr.Records("a"))
}

// TestDuplicateIntent 覆盖重复 Intent 的重复计数（i>p 崩溃重做）。
func TestDuplicateIntent(t *testing.T) {
	jr, ef := journal.New(), effect.New()
	m := saga.New(jr, ef)
	if err := m.Begin("d", 5, 2, 1); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := m.Intent("d", i); err != nil {
			t.Fatal(err)
		}
		if err := m.Done("d", i); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Intent("d", 3); err != nil { // I3 后“崩溃”
		t.Fatal(err)
	}
	pl, err := m.Recover("d")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: I3 后崩溃；输出: %v；依据: i=3>p=2 结果未知重做", pl)
	if pl != (saga.Plan{Kind: saga.PlanForward, Step: 3}) {
		t.Fatalf("plan=%v", pl)
	}
	if err := m.Intent("d", 3); err != nil {
		t.Fatal(err)
	}
	added, repeated := ef.Stats(effect.Key{ID: "d", Step: 3, Phase: effect.Fwd})
	t.Logf("fwd(3): 新增 %d 重复 %d", added, repeated)
	if added != 1 || repeated != 1 {
		t.Fatalf("fwd(3) 新增=%d 重复=%d, 期望 1/1", added, repeated)
	}
}

// TestConcurrent 多实例并发推进 + 同实例并发 Recover，-race 下运行。
func TestConcurrent(t *testing.T) {
	jr, ef := journal.New(), effect.New()
	m := saga.New(jr, ef)
	const instances = 32
	var wg sync.WaitGroup
	for k := 0; k < instances; k++ {
		id := fmt.Sprintf("c%d", k)
		if err := m.Begin(id, 4, 1, 1); err != nil {
			t.Fatal(err)
		}
		wg.Add(2)
		go func(id string) { // 推进到终态
			defer wg.Done()
			for {
				pl, err := m.Recover(id)
				if err != nil {
					t.Error(err)
					return
				}
				switch pl.Kind {
				case saga.PlanForward:
					if err := m.Intent(id, pl.Step); err == nil {
						_ = m.Done(id, pl.Step)
					}
				case saga.PlanCompensate:
					if err := m.CompIntent(id, pl.Step); err == nil {
						_ = m.CompDone(id, pl.Step)
					}
				case saga.PlanProbe:
					_ = m.Probe(id, true)
				default:
					return
				}
			}
		}(id)
		go func(id string) { // 并发只读恢复
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, err := m.Recover(id); err != nil {
					t.Error(err)
					return
				}
			}
		}(id)
	}
	wg.Wait()
	for k := 0; k < instances; k++ {
		id := fmt.Sprintf("c%d", k)
		pl, _ := m.Recover(id)
		// 并发 Recover 可能放弃在途意图，使实例合法地走向已补偿。
		if pl.Kind != saga.PlanFinished && pl.Kind != saga.PlanCompensated {
			t.Fatalf("%s 终态=%v, 期望已完成或已补偿", id, pl)
		}
	}
	t.Log("32 实例并发推进全部到达终态（已完成或已补偿）；并发 Recover 无竞态")
}

// TestDeterminism 相同动作序列重放得到相同日志、计划与副作用计数。
func TestDeterminism(t *testing.T) {
	run := func() ([]journal.Record, saga.Plan, int, int) {
		jr, ef := journal.New(), effect.New()
		m := saga.New(jr, ef)
		_ = m.Begin("z", 4, 1, 1)
		_ = m.Intent("z", 0)
		_ = m.Fail("z", 0, journal.Transient)
		_ = m.Intent("z", 0)
		_ = m.Done("z", 0)
		_ = m.Intent("z", 1)
		pl, _ := m.Recover("z")
		fa, fr := ef.Totals("z", effect.Fwd)
		return jr.Records("z"), pl, fa, fr
	}
	log1, pl1, a1, r1 := run()
	log2, pl2, a2, r2 := run()
	if fmt.Sprint(log1) != fmt.Sprint(log2) || pl1 != pl2 || a1 != a2 || r1 != r2 {
		t.Fatalf("重放不一致: %v/%v vs %v/%v", log1, pl1, log2, pl2)
	}
	t.Logf("两次重放日志、计划 %v、计数(%d,%d) 完全一致", pl1, a1, r1)
}
