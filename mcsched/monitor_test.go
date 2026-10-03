package mcsched

import (
	"strings"
	"testing"
)

// traceString 收集 [0, n) 每个 tick 运行的任务编号，空闲记为 '.'。
func traceString(m *Monitor, n int) string {
	var b strings.Builder
	for t := 0; t < n; t++ {
		id, ok := m.RunAt(t)
		if !ok {
			b.WriteByte('.')
		} else {
			b.WriteString(id)
		}
	}
	return b.String()
}

func TestSpecExampleOverrun(t *testing.T) {
	m := New()
	mustAdd(t, m, Task{ID: "h", Lvl: HI, CL: 2, CH: 5, T: 10, Prio: 1, Phi: 0})
	mustAdd(t, m, Task{ID: "l", Lvl: LO, CL: 2, CH: 2, T: 4, Prio: 2, Phi: 0})
	if err := m.SetDemand("h", 0, 5); err != nil {
		t.Fatalf("SetDemand: %v", err)
	}
	if err := m.Step(12); err != nil {
		t.Fatalf("Step: %v", err)
	}
	// 判定依据：t0,t1 运行 h（tick1 末 exec==CL=2 且 rem=3 不切换，
	// 切换发生在 tick2 运行之后），t2 tick 末切换并舍弃 l 的首个作业；
	// t4 的 l 在 HI 模式被跳过；h 于 t4 tick 完成；t5 空闲时恢复 LO；
	// l 于 t8 再次释放，t8,t9,t10,t11 运行 l。
	got := traceString(m, 12)
	want := "hhhhh...llhh"
	if got != want {
		t.Fatalf("trace=%q want %q", got, want)
	}
	st := m.Stats()
	if st.Switches != 1 || st.Recoveries != 1 || st.Discarded != 1 ||
		st.Skipped != 1 || st.MissedLO != 0 || st.MissedHI != 0 || st.Completions != 3 {
		t.Fatalf("stats=%+v", st)
	}
	if m.Mode() != ModeLO {
		t.Fatalf("mode=%v want LO", m.Mode())
	}
}

func mustAdd(t *testing.T, m *Monitor, task Task) {
	t.Helper()
	if err := m.AddTask(task); err != nil {
		t.Fatalf("AddTask(%s): %v", task.ID, err)
	}
}

func TestSpecExampleExactCLNoSwitch(t *testing.T) {
	m := New()
	mustAdd(t, m, Task{ID: "h", Lvl: HI, CL: 2, CH: 5, T: 10, Prio: 1, Phi: 0})
	mustAdd(t, m, Task{ID: "l", Lvl: LO, CL: 2, CH: 2, T: 4, Prio: 2, Phi: 0})
	if err := m.SetDemand("h", 0, 2); err != nil {
		t.Fatalf("SetDemand: %v", err)
	}
	if err := m.Step(12); err != nil {
		t.Fatalf("Step: %v", err)
	}
	// 判定依据：实际执行量恰等于 CL，h 在 t1 tick 末完成，不触发切换；
	// l 首个作业 t2,t3 运行；t4 释放的第二个 l 于 t6,t7 运行；
	// 第二个 h 于 t8 释放，抢占 t8 开始的 l，t8,t9 为 h。
	got := traceString(m, 12)
	want := "hhllll..llhh"
	if got != want {
		t.Fatalf("trace=%q want %q", got, want)
	}
	st := m.Stats()
	if st.Switches != 0 || st.Recoveries != 0 || st.Discarded != 0 ||
		st.Skipped != 0 || st.MissedLO != 0 || st.MissedHI != 0 || st.Completions != 5 {
		t.Fatalf("stats=%+v", st)
	}
	if m.Mode() != ModeLO {
		t.Fatalf("mode=%v want LO", m.Mode())
	}
}

func TestSpecExampleCompletionAtDeadlineRecoveryFirst(t *testing.T) {
	m := New()
	mustAdd(t, m, Task{ID: "h", Lvl: HI, CL: 2, CH: 4, T: 4, Prio: 1, Phi: 0})
	mustAdd(t, m, Task{ID: "l", Lvl: LO, CL: 2, CH: 2, T: 4, Prio: 2, Phi: 0})
	if err := m.SetDemand("h", 0, 4); err != nil {
		t.Fatalf("SetDemand: %v", err)
	}
	if err := m.Step(12); err != nil {
		t.Fatalf("Step: %v", err)
	}
	// 判定依据：t2 tick 末 h exec==CL=2 且未完成 -> 切 HI、舍弃 t0 释放的 l；
	// h 于 t3 tick 末（时刻 4 的第一步之前）完成；时刻 4 先因空闲恢复 LO，
	// 再释放 l 与新 h；h 优先运行 t4,t5，l 在 t6,t7 运行；依此循环。
	got := traceString(m, 12)
	// t0..t3 为首个 h（t1 末切换并舍弃 l，t3 末完成）；时刻 4 先恢复 LO
	// 再释放 l 与第二个 h（后者实际量=CL=2，t4,t5 完成）；l t6,t7 完成；
	// 时刻 8 再释放一对，h t8,t9 完成，l t10,t11 完成。
	want := "hhhhhhllhhll"
	if got != want {
		t.Fatalf("trace=%q want %q", got, want)
	}
	st := m.Stats()
	if st.Switches != 1 || st.Recoveries != 1 || st.Discarded != 1 ||
		st.Skipped != 0 || st.MissedHI != 0 || st.Completions != 5 {
		t.Fatalf("stats=%+v", st)
	}
}

func TestSameTickReleaseDiscardedImmediately(t *testing.T) {
	// h 与 l 同时释放，且 l 周期为 3：h 在 t2 tick 末切换时，
	// t0 释放的 l 尚未运行，立即被舍弃（Discarded=1，而非错过）。
	m := New()
	mustAdd(t, m, Task{ID: "h", Lvl: HI, CL: 2, CH: 4, T: 10, Prio: 1, Phi: 0})
	mustAdd(t, m, Task{ID: "l", Lvl: LO, CL: 1, CH: 1, T: 3, Prio: 2, Phi: 0})
	if err := m.SetDemand("h", 0, 4); err != nil {
		t.Fatalf("SetDemand: %v", err)
	}
	if err := m.Step(6); err != nil {
		t.Fatalf("Step: %v", err)
	}
	got := traceString(m, 6)
	want := "hhhh.." // t0..t3 h（t1 末切换并舍弃 l），t3 末完成；t4 空闲恢复，l 在 t3 被跳过
	if got != want {
		t.Fatalf("trace=%q want %q", got, want)
	}
	st := m.Stats()
	if st.Discarded != 1 || st.Skipped != 1 || st.MissedLO != 0 || st.Switches != 1 {
		t.Fatalf("stats=%+v", st)
	}
}

func TestHIJobInsideHIModeDoesNotRetrigger(t *testing.T) {
	// 两个 HI 任务：h1 先引发切换；切换后 h2 的 exec 越过自身 CL，
	// 不再增加切换计数。
	m := New()
	mustAdd(t, m, Task{ID: "a", Lvl: HI, CL: 1, CH: 3, T: 20, Prio: 1, Phi: 0})
	mustAdd(t, m, Task{ID: "b", Lvl: HI, CL: 2, CH: 5, T: 20, Prio: 2, Phi: 0})
	if err := m.SetDemand("a", 0, 3); err != nil {
		t.Fatalf("SetDemand(a): %v", err)
	}
	if err := m.SetDemand("b", 0, 5); err != nil {
		t.Fatalf("SetDemand(b): %v", err)
	}
	if err := m.Step(8); err != nil {
		t.Fatalf("Step: %v", err)
	}
	// a 在 t1 tick 末 exec==CL=1 -> 切换（b 同刻未运行，无 LO 作业）；
	// 此后 a 于 t2 完成，b 运行 t3..t7，越过 CL=2 不再切换。
	got := traceString(m, 8)
	want := "aaabbbbb"
	if got != want {
		t.Fatalf("trace=%q want %q", got, want)
	}
	if st := m.Stats(); st.Switches != 1 || st.Recoveries != 0 || st.Completions != 2 {
		t.Fatalf("stats=%+v", st)
	}
	if m.Mode() != ModeHI {
		t.Fatalf("mode=%v want HI", m.Mode())
	}
}
