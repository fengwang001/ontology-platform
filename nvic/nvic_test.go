package nvic

import (
	"errors"
	"sync"
	"testing"
)

// 场景一：同组仅子优先级更小的挂起中断不抢占；
// 但在选候选时子优先级更小者先进入。
// s=1：irq0=0(g0,sub0)，irq1=1(g0,sub1)，
// irq3=0(g0,sub0)，irq2=2(g1,sub0)。
func TestScenarioSameGroupSubPriority(t *testing.T) {
	runScenario(t, 4, 1, []Op{
		{Kind: opSetPriority, IRQ: 1, Arg: 1},
		{Kind: opSetPriority, IRQ: 2, Arg: 2},
		{Kind: opEnable, IRQ: 0},
		{Kind: opPend, IRQ: 0}, // Enter(0)
		{Kind: opEnable, IRQ: 1},
		{Kind: opPend, IRQ: 1}, // 同组 g0：不抢占
		{Kind: opEnable, IRQ: 3},
		{Kind: opPend, IRQ: 3}, // 同组 g0：不抢占
		{Kind: opEnable, IRQ: 2},
		{Kind: opPend, IRQ: 2}, // g1 更大：不抢占
		{Kind: opReturn},       // Exit(0), TailChain(3)：sub0 先于 sub1
		{Kind: opReturn},       // Exit(3), TailChain(1)
		{Kind: opReturn},       // Exit(1), TailChain(2)
		{Kind: opReturn},       // Exit(2), Idle
	}, true)
}

// 场景二：阈值按组优先级比较；组优先级恰等于 g(b) 也被屏蔽；
// b=0 不屏蔽。
func TestScenarioBaseMaskByGroup(t *testing.T) {
	runScenario(t, 2, 1, []Op{
		{Kind: opSetPriority, IRQ: 0, Arg: 2}, // g1
		{Kind: opSetPriority, IRQ: 1, Arg: 3}, // g1
		{Kind: opSetBase, Arg: 2},             // g(b)=1
		{Kind: opEnable, IRQ: 0},
		{Kind: opPend, IRQ: 0}, // g1 >= g(b)=1：被屏蔽，无事件
		{Kind: opEnable, IRQ: 1},
		{Kind: opPend, IRQ: 1},    // 同样被屏蔽
		{Kind: opSetBase, Arg: 3}, // g(3)=1，恰等于仍屏蔽
		{Kind: opSetBase, Arg: 0}, // 解除屏蔽：候选 0 进入
		{Kind: opReturn},          // Exit(0)，栈空 TailChain(1)
		{Kind: opReturn},          // Exit(1), Idle
	}, true)
}

// 场景三：从线程态尾链——Return 后栈清空时直接进入候选，
// 事件是 TailChain 而非 Enter。
func TestScenarioTailChainFromThread(t *testing.T) {
	runScenario(t, 2, 1, []Op{
		{Kind: opEnable, IRQ: 0},
		{Kind: opPend, IRQ: 0}, // Enter(0)
		{Kind: opEnable, IRQ: 1},
		{Kind: opPend, IRQ: 1}, // 默认同组 g0，不抢占
		{Kind: opReturn},       // Exit(0), TailChain(1)
		{Kind: opReturn},       // Exit(1), Idle
	}, true)
}

// 场景四：返回被抢占的处理器时 Resume 与尾链的分流。
// s=2：irq0=0x40(g16)，irq1=0x10(g4)，irq2=0x20(g8)。
func TestScenarioResumeVsTailChain(t *testing.T) {
	runScenario(t, 3, 2, []Op{
		{Kind: opSetPriority, IRQ: 0, Arg: 0x40},
		{Kind: opSetPriority, IRQ: 1, Arg: 0x10},
		{Kind: opSetPriority, IRQ: 2, Arg: 0x20},
		{Kind: opEnable, IRQ: 0},
		{Kind: opPend, IRQ: 0}, // Enter(0), level g16
		{Kind: opEnable, IRQ: 1},
		{Kind: opPend, IRQ: 1}, // Preempt(1), level g4
		{Kind: opEnable, IRQ: 2},
		{Kind: opPend, IRQ: 2}, // g8 不严格小于 g4，不抢占
		{Kind: opReturn},       // Exit(1)；新顶 g16，g8<16 → TailChain(2)
		{Kind: opReturn},       // Exit(2)；Resume(0)
		{Kind: opReturn},       // Exit(0), Idle
	}, true)
}

// 场景五：与恢复处理器同组的挂起中断必须等其退出后才进入。
// irq0=0x10(g4)，irq1=0x11(g4,sub1)，irq2=0x04(g1)。
func TestScenarioSameGroupWaitsForResumeExit(t *testing.T) {
	runScenario(t, 3, 2, []Op{
		{Kind: opSetPriority, IRQ: 0, Arg: 0x10},
		{Kind: opSetPriority, IRQ: 1, Arg: 0x11},
		{Kind: opSetPriority, IRQ: 2, Arg: 0x04},
		{Kind: opEnable, IRQ: 0},
		{Kind: opPend, IRQ: 0}, // Enter(0), level g4
		{Kind: opEnable, IRQ: 2},
		{Kind: opPend, IRQ: 2}, // Preempt(2), level g1
		{Kind: opEnable, IRQ: 1},
		{Kind: opPend, IRQ: 1}, // g4 不小于 g1，保持挂起
		{Kind: opReturn},       // Exit(2)；新顶 g4，非严格更小 → Resume(0)
		{Kind: opReturn},       // Exit(0)；栈空 → TailChain(1)
		{Kind: opReturn},       // Exit(1), Idle
	}, true)
}

// 场景六：活动中的中断再次 Pend，退出后立即尾链进入它自己。
func TestScenarioRependActiveSelfTailChain(t *testing.T) {
	runScenario(t, 1, 0, []Op{
		{Kind: opEnable, IRQ: 0},
		{Kind: opPend, IRQ: 0}, // Enter(0)
		{Kind: opPend, IRQ: 0}, // 已活动：挂起置位但非候选，无事件
		{Kind: opReturn},       // Exit(0), TailChain(0)
		{Kind: opReturn},       // Exit(0), Idle
	}, true)
}

// 场景七：SetPriority 提升挂起中断（数值变小）后立即抢占。
func TestScenarioSetPriorityImmediatePreempt(t *testing.T) {
	runScenario(t, 2, 2, []Op{
		{Kind: opSetPriority, IRQ: 0, Arg: 0x40}, // g16
		{Kind: opSetPriority, IRQ: 1, Arg: 0x80}, // g32
		{Kind: opEnable, IRQ: 0},
		{Kind: opPend, IRQ: 0}, // Enter(0)
		{Kind: opEnable, IRQ: 1},
		{Kind: opPend, IRQ: 1},                   // g32 不抢占
		{Kind: opSetPriority, IRQ: 1, Arg: 0x04}, // g1 < g16 → Preempt(1)
		{Kind: opReturn},                         // Exit(1), Resume(0)
		{Kind: opReturn},                         // Exit(0), Idle
	}, true)
}

// 场景八：Disable 与 Clear 不影响已活动的中断。
func TestScenarioDisableClearDoNotTouchActive(t *testing.T) {
	// 活动中再挂起后 Disable，退出时因失能不尾链；
	// 但挂起位保留，重新使能立即进入。Clear 同理不清活动标志。
	runScenario(t, 1, 0, []Op{
		{Kind: opEnable, IRQ: 0},
		{Kind: opPend, IRQ: 0},    // Enter(0)
		{Kind: opPend, IRQ: 0},    // 活动中再挂起
		{Kind: opDisable, IRQ: 0}, // 不影响活动标志，无事件
		{Kind: opReturn},          // Exit(0)；失能故非候选 → Idle，挂起位仍在
		{Kind: opEnable, IRQ: 0},  // 挂起仍有效 → Enter(0)
		{Kind: opClear, IRQ: 0},   // 活动中 Clear 不影响活动，无事件
		{Kind: opReturn},          // Exit(0)，挂起已 Clear → Idle
	}, true)
}

// 场景九：构造与参数校验，拒绝时状态不变。
func TestScenarioValidation(t *testing.T) {
	if _, err := New(0, 0); !errors.Is(err, ErrInvalidInterruptCount) {
		t.Fatalf("New(0,0) err=%v", err)
	}
	if _, err := New(2, -1); !errors.Is(err, ErrInvalidSubPriority) {
		t.Fatalf("New(2,-1) err=%v", err)
	}
	if _, err := New(2, 8); !errors.Is(err, ErrInvalidSubPriority) {
		t.Fatalf("New(2,8) err=%v", err)
	}

	n, _ := New(2, 1)
	if _, err := n.Enable(2); !errors.Is(err, ErrIRQOutOfRange) {
		t.Fatalf("Enable(2) err=%v", err)
	}
	if _, err := n.Pend(-1); !errors.Is(err, ErrIRQOutOfRange) {
		t.Fatalf("Pend(-1) err=%v", err)
	}
	// 编号越界优先于优先级非法。
	if _, err := n.SetPriority(9, 256); !errors.Is(err, ErrIRQOutOfRange) {
		t.Fatalf("SetPriority(9,256) err=%v", err)
	}
	if _, err := n.SetPriority(0, 256); !errors.Is(err, ErrInvalidPriority) {
		t.Fatalf("SetPriority(0,256) err=%v", err)
	}
	if _, err := n.SetPriority(0, -1); !errors.Is(err, ErrInvalidPriority) {
		t.Fatalf("SetPriority(0,-1) err=%v", err)
	}
	if _, err := n.SetBase(256); !errors.Is(err, ErrInvalidBase) {
		t.Fatalf("SetBase(256) err=%v", err)
	}
	if _, err := n.Return(); !errors.Is(err, ErrReturnFromThread) {
		t.Fatalf("Return on empty err=%v", err)
	}

	// 被拒绝的操作不得改变任何状态：合法操作随后仍按初始状态工作。
	evs, err := n.Enable(0)
	if err != nil || len(evs) != 0 {
		t.Fatalf("Enable(0) after rejects: evs=%v err=%v", evs, err)
	}
	evs, err = n.Pend(0)
	if err != nil || len(evs) != 1 || evs[0] != (Event{Kind: Enter, IRQ: 0}) {
		t.Fatalf("Pend(0) after rejects: evs=%v err=%v", evs, err)
	}
}

// 事件可读表示（日志使用）。
func TestEventString(t *testing.T) {
	cases := map[Event]string{
		{Kind: Enter, IRQ: 2}:     "Enter(2)",
		{Kind: Preempt, IRQ: 0}:   "Preempt(0)",
		{Kind: Exit, IRQ: 1}:      "Exit(1)",
		{Kind: TailChain, IRQ: 3}: "TailChain(3)",
		{Kind: Resume, IRQ: 1}:    "Resume(1)",
		{Kind: Idle, IRQ: -1}:     "Idle",
	}
	for e, want := range cases {
		if got := e.String(); got != want {
			t.Fatalf("%v.String()=%q want %q", e, got, want)
		}
	}
}

// 相同操作序列重放必须得到完全相同的事件序列。
func TestReplayDeterminism(t *testing.T) {
	ops := []Op{
		{Kind: opSetPriority, IRQ: 0, Arg: 0x40},
		{Kind: opSetPriority, IRQ: 1, Arg: 0x10},
		{Kind: opSetPriority, IRQ: 2, Arg: 0x20},
		{Kind: opEnable, IRQ: 0}, {Kind: opPend, IRQ: 0},
		{Kind: opEnable, IRQ: 1}, {Kind: opPend, IRQ: 1},
		{Kind: opEnable, IRQ: 2}, {Kind: opPend, IRQ: 2},
		{Kind: opReturn}, {Kind: opReturn}, {Kind: opReturn},
	}
	collect := func() []Event {
		var all []Event
		n, _ := New(3, 2)
		for _, o := range ops {
			evs, err := applyOnReal(n, o)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			all = append(all, evs...)
		}
		return all
	}
	first := collect()
	for k := 0; k < 5; k++ {
		if again := collect(); !eventsEqual(first, again) {
			t.Fatalf("replay %d mismatch: %v vs %v", k, again, first)
		}
	}
}

// 并发调用：结果等价于某个串行顺序；-race 下无数据竞争，
// 每次操作返回后不变量保持成立。
func TestConcurrentOperations(t *testing.T) {
	n, err := New(4, 2)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 300; k++ {
				irq := (g + k) % 4
				switch k % 9 {
				case 0:
					n.Enable(irq)
				case 1:
					n.Disable(irq)
				case 2:
					n.Pend(irq)
				case 3:
					n.Clear(irq)
				case 4:
					n.SetPriority(irq, (g*17+k*7)&0xFF)
				case 5:
					n.SetBase((k * 3) & 0xFF)
				case 6:
					n.Return() // 可能栈空报错，属正常
				case 7:
					n.Stack()
					n.RunningLevel()
				case 8:
					n.Active(irq)
					n.Pending(irq)
					n.Base()
				}
				// 每次操作后：活动集合恰好等于栈集合且无重复。
				// 该不变量是操作完成时刻的性质，须在单次持锁快照内校验。
				n.mu.Lock()
				seen := map[int]bool{}
				for _, v := range n.stack {
					if seen[v] {
						t.Errorf("duplicate %d in stack %v", v, n.stack)
					}
					seen[v] = true
					if !n.active[v] {
						t.Errorf("stack irq %d not active", v)
					}
				}
				for v, a := range n.active {
					if a && !seen[v] {
						t.Errorf("irq %d active but not on stack %v", v, n.stack)
					}
				}
				n.mu.Unlock()
			}
		}(g)
	}
	wg.Wait()
}
