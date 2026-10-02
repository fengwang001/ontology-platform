package canfault

import (
	"errors"
	"sync"
	"testing"
)

// TestNaiveCrossCheck 用逐步朴素模拟器对照一条覆盖全部规则的混合脚本。
func TestNaiveCrossCheck(t *testing.T) {
	script := buildCrossCheckScript()

	c := New()
	m := newNaiveModel()
	for i, op := range script {
		var got, want error
		switch op.kind {
		case "apply":
			got = c.Apply(op.ev)
		case "restart":
			got = c.Restart()
		case "idle11":
			got = c.Idle11()
		}
		want = m.step(op)
		if !errors.Is(got, want) {
			t.Fatalf("第 %d 步 %v: Controller err=%v, 朴素模型 err=%v", i+1, op, got, want)
		}

		ms := m.snapshot()
		cs := c.Snapshot()
		t.Logf("对照第%d步 op=%s ev=%s | err=%s | Controller{TEC=%d REC=%d %s rec=%v idle=%d seq=%d log=%d} 朴素{TEC=%d REC=%d %s rec=%v idle=%d seq=%d log=%d} | 判定: %s",
			i+1, op.kind, op.ev, errText(got),
			cs.TEC, cs.REC, cs.State, cs.Recovering, cs.IdleCount, cs.OpSeq, len(cs.Transitions),
			ms.tec, ms.rec, ms.state, ms.recovering, ms.idleCount, ms.opSeq, len(ms.transitions),
			verdict(got))

		if cs.TEC != ms.tec || cs.REC != ms.rec || cs.State != ms.state ||
			cs.Recovering != ms.recovering || cs.IdleCount != ms.idleCount || cs.OpSeq != ms.opSeq {
			t.Fatalf("第 %d 步计数/状态/序号不一致", i+1)
		}
		if len(cs.Transitions) != len(ms.transitions) {
			t.Fatalf("第 %d 步迁移记录长度不一致: %d vs %d", i+1, len(cs.Transitions), len(ms.transitions))
		}
		for j, tr := range cs.Transitions {
			if tr != ms.transitions[j] {
				t.Fatalf("第 %d 步第 %d 条迁移不一致: %+v vs %+v", i+1, j, tr, ms.transitions[j])
			}
		}
	}
}

func verdict(err error) string {
	if err != nil {
		return "拒绝，原因=" + err.Error()
	}
	return "接受"
}

// buildCrossCheckScript 构造覆盖全部计数规则、恢复流程与拒绝路径的混合脚本。
func buildCrossCheckScript() []naiveOp {
	var script []naiveOp
	ev := func(e Event) naiveOp { return naiveOp{kind: "apply", ev: e} }

	script = append(script, ev(TxOK), ev(RxOK))  // 计数为 0 时不下溢
	script = append(script, ev(RxErr), ev(RxOK)) // 1 再减回 0
	script = append(script, ev(TxErr), ev(TxOK)) // 8 再减到 7
	script = append(script, ev(TxAckErr))        // 主动 +8 -> 15

	// 推进 REC 到 130：16 个 RxErrDominant(->128) + 2 个 RxErr(->130)，再 RxOK 置 127。
	for i := 0; i < 16; i++ {
		script = append(script, ev(RxErrDominant))
	}
	script = append(script, ev(RxErr), ev(RxErr), ev(RxOK)) // 130 -> 127 主动
	script = append(script, ev(RxErr))                      // 127 -> 128 被动
	script = append(script, ev(RxErrDominant))              // 128 -> 136
	for i := 0; i < 9; i++ {                                // 136 -> 127
		script = append(script, ev(RxOK))
	}
	script = append(script, ev(RxOK)) // 127 -> 126（走减一分支而非置位分支）

	// TEC 被动态 TxAckErr 不增：先把 TEC 推到 128。
	for i := 0; i < 15; i++ { // 当前 TEC=15，+120 -> 135
		script = append(script, ev(TxErr))
	}
	script = append(script, ev(TxAckErr)) // 被动，计数不变 135
	script = append(script, ev(TxOK))     // 135 -> 134

	// TEC 247 -> 255 仍被动，255 -> 263 进入总线关闭。
	// 当前 TEC=134：14 次 TxErr -> 246，1 次 TxOK -> 247，TxErr -> 255（仍被动），TxErr -> 263（总线关闭）。
	for i := 0; i < 14; i++ {
		script = append(script, ev(TxErr))
	}
	script = append(script, ev(TxOK))  // 246 -> 247
	script = append(script, ev(TxErr)) // 247 -> 255，仍被动
	script = append(script, ev(TxErr)) // 255 -> 263，总线关闭

	// 总线关闭期间的拒绝路径。
	script = append(script,
		ev(TxOK), ev(RxErrDominant),
		naiveOp{kind: "apply", ev: Event(-1)}, // 非法事件先判非法，即使已总线关闭
		naiveOp{kind: "idle11"},               // 未恢复时 Idle11 被拒
	)

	// 恢复流程：Restart 成功、中途再 Restart 被拒、128 次 Idle11 完成。
	script = append(script, naiveOp{kind: "restart"})
	script = append(script, naiveOp{kind: "restart"}) // 恢复中，拒绝
	for i := 0; i < 128; i++ {
		script = append(script, naiveOp{kind: "idle11"})
	}
	// 恢复结束后 Idle11 被拒；Restart 被拒（非总线关闭）；Apply 恢复正常。
	script = append(script, naiveOp{kind: "idle11"})
	script = append(script, naiveOp{kind: "restart"})
	script = append(script, ev(TxErr), ev(RxErr), ev(RxOK))

	return script
}

// TestDeterministicReplay 覆盖相同事件序列重放得到完全相同结果。
func TestDeterministicReplay(t *testing.T) {
	script := buildCrossCheckScript()
	run := func() Snapshot {
		c := New()
		for _, op := range script {
			var err error
			switch op.kind {
			case "apply":
				err = c.Apply(op.ev)
			case "restart":
				err = c.Restart()
			case "idle11":
				err = c.Idle11()
			}
			if err != nil && !isRejected(err) {
				t.Fatalf("意外错误: %v", err)
			}
		}
		return c.Snapshot()
	}
	first := run()
	for k := 0; k < 3; k++ {
		again := run()
		if again.TEC != first.TEC || again.REC != first.REC || again.State != first.State ||
			again.Recovering != first.Recovering || again.IdleCount != first.IdleCount ||
			again.OpSeq != first.OpSeq || len(again.Transitions) != len(first.Transitions) {
			t.Fatalf("第 %d 次重放不一致", k+1)
		}
		for i, tr := range again.Transitions {
			if tr != first.Transitions[i] {
				t.Fatalf("第 %d 次重放迁移记录第 %d 条不一致", k+1, i)
			}
		}
	}
	t.Logf("确定性重放 | 最终 TEC=%d REC=%d %s seq=%d log=%d | 判定: 4 次重放完全一致",
		first.TEC, first.REC, first.State, first.OpSeq, len(first.Transitions))
}

func isRejected(err error) bool {
	return errors.Is(err, ErrInvalidEvent) || errors.Is(err, ErrApplyWhileBusOff) ||
		errors.Is(err, ErrRestartNotBusOff) || errors.Is(err, ErrRestartAlreadyRecovering) ||
		errors.Is(err, ErrIdle11NotRecovering)
}

// TestConcurrentOps 验证并发调用可串行化：结果与单线程重放一致且状态不变量恒成立。
func TestConcurrentOps(t *testing.T) {
	// 每个 goroutine 使用独立的、确定的操作脚本；最终状态必须等于这些脚本
	// 按 goroutine 编号顺序串行执行的结果（互斥锁保证某种串行顺序）。
	type task struct {
		kind string
		ev   Event
	}
	scripts := [][]task{
		{{"apply", TxErr}, {"apply", TxErr}, {"apply", TxOK}},
		{{"apply", RxErrDominant}, {"apply", RxOK}, {"apply", RxErr}},
		{{"apply", TxAckErr}, {"apply", TxErr}},
	}

	c := New()
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var readerWG sync.WaitGroup

	// 读快照的 goroutine，持续校验状态不变量。
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s := c.Snapshot()
				switch s.State {
				case ErrorActive, ErrorPassive:
					if s.TEC > 255 {
						t.Errorf("非总线关闭时 TEC=%d 超过 255", s.TEC)
						return
					}
				case BusOff:
					if s.TEC <= 255 {
						t.Errorf("总线关闭时 TEC=%d 应大于 255（恢复结束前）", s.TEC)
						return
					}
				}
				for i := 1; i < len(s.Transitions); i++ {
					if s.Transitions[i-1].New != s.Transitions[i].Old {
						t.Errorf("并发下迁移链断裂: %+v", s.Transitions)
						return
					}
				}
			}
		}
	}()

	// 写 goroutine。
	for gi, sc := range scripts {
		wg.Add(1)
		go func(id int, ops []task) {
			defer wg.Done()
			for _, op := range ops {
				switch op.kind {
				case "apply":
					_ = c.Apply(op.ev)
				}
			}
		}(gi, sc)
	}
	wg.Wait()
	close(stop)
	readerWG.Wait()

	// 串行参照：这些事件只涉及 active 态的计数加减，与顺序无关地可核对计数。
	var tecDelta, recDelta int
	for _, sc := range scripts {
		for _, op := range sc {
			switch op.ev {
			case TxErr:
				tecDelta += 8
			case TxOK:
				tecDelta-- // TEC 起始 0，第一次 TxOK 前已有 TxErr，各脚本内部不会下溢
			case TxAckErr:
				tecDelta += 8
			case RxErrDominant:
				recDelta += 8
			case RxErr:
				recDelta++
			case RxOK:
				recDelta--
			}
		}
	}
	s := c.Snapshot()
	if s.TEC != tecDelta {
		t.Fatalf("并发后 TEC=%d，期望 %d（等价某种串行顺序）", s.TEC, tecDelta)
	}
	if s.REC != recDelta {
		t.Fatalf("并发后 REC=%d，期望 %d", s.REC, recDelta)
	}
	t.Logf("并发混合读写 | TEC=%d REC=%d %s | 判定: 计数与串行计算一致，不变量恒成立",
		s.TEC, s.REC, s.State)
}
