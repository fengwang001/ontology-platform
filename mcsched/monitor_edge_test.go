package mcsched

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// 错过先于恢复判定：d=t 的作业先被移除；错过后恰好空闲，则同一 tick
// 第二步恢复 LO。
func TestMissBeforeRecovery(t *testing.T) {
	m := New()
	// h 在 t0 tick 末引发切换；g 于 t1 释放，需求 3，t1..t3 完成；
	// h 到时刻 4（d=4）仍未完成 -> MissedHI；此刻 g 已不存在，系统空闲
	// -> 同 tick 恢复 LO，随后释放新 h 并运行。
	mustAdd(t, m, Task{ID: "h", Lvl: HI, CL: 1, CH: 4, T: 4, Prio: 2, Phi: 0})
	mustAdd(t, m, Task{ID: "g", Lvl: HI, CL: 4, CH: 4, T: 4, Prio: 1, Phi: 1})
	if err := m.SetDemand("h", 0, 4); err != nil {
		t.Fatalf("SetDemand(h): %v", err)
	}
	if err := m.SetDemand("g", 0, 3); err != nil {
		t.Fatalf("SetDemand(g): %v", err)
	}
	if err := m.Step(5); err != nil {
		t.Fatalf("Step: %v", err)
	}
	got := traceString(m, 5)
	want := "hgggh"
	if got != want {
		t.Fatalf("trace=%q want %q（判定：t4 先错过 h 再空闲恢复）", got, want)
	}
	st := m.Stats()
	// t4 恢复 LO 后释放的新 h 需求默认 CL=1，在 LO 下 exec==CL 完成，不切换。
	if st.MissedHI != 1 || st.Recoveries != 1 || st.Switches != 1 ||
		st.Completions != 2 {
		t.Fatalf("stats=%+v", st)
	}
	if m.Mode() != ModeLO {
		t.Fatalf("mode=%v want LO", m.Mode())
	}
}

// HI 作业在 HI 模式下到 d 仍未完成，同样计 MissedHI；错过后若仍有
// 未完成作业则不恢复。
func TestHIMissedInHIMode(t *testing.T) {
	m := New()
	mustAdd(t, m, Task{ID: "h", Lvl: HI, CL: 1, CH: 3, T: 3, Prio: 2, Phi: 0})
	mustAdd(t, m, Task{ID: "g", Lvl: HI, CL: 3, CH: 3, T: 6, Prio: 1, Phi: 1})
	if err := m.SetDemand("h", 0, 3); err != nil {
		t.Fatalf("SetDemand(h): %v", err)
	}
	if err := m.Step(4); err != nil {
		t.Fatalf("Step: %v", err)
	}
	got := traceString(m, 4)
	want := "hggg"
	if got != want {
		t.Fatalf("trace=%q want %q", got, want)
	}
	st := m.Stats()
	if st.MissedHI != 1 || st.Switches != 1 || st.Recoveries != 0 ||
		st.Completions != 1 {
		t.Fatalf("stats=%+v（HI 模式错过后仍有 g，不应恢复）", st)
	}
	if m.Mode() != ModeHI {
		t.Fatalf("mode=%v want HI", m.Mode())
	}
}

// HI 模式下 LO 释放被跳过：跳过计数加一、作业序号照占；对被跳过作业
// 预设的需求无效果（不产生作业、不计完成/错过）。
func TestSkippedJobIndexConsumed(t *testing.T) {
	m := New()
	mustAdd(t, m, Task{ID: "h", Lvl: HI, CL: 1, CH: 4, T: 10, Prio: 1, Phi: 0})
	mustAdd(t, m, Task{ID: "l", Lvl: LO, CL: 2, CH: 2, T: 3, Prio: 2, Phi: 0})
	if err := m.SetDemand("h", 0, 4); err != nil {
		t.Fatalf("SetDemand(h): %v", err)
	}
	// l 的 k=1 将于 t=3 在 HI 模式下被跳过：设置成功，但该需求永不生效。
	if err := m.SetDemand("l", 1, 1); err != nil {
		t.Fatalf("SetDemand(l,1): %v", err)
	}
	if err := m.Step(12); err != nil {
		t.Fatalf("Step: %v", err)
	}
	got := traceString(m, 12)
	// 逐 tick：h0 t0 末切换舍弃 l0；h t1..t3 完成，t3 跳过 l(k=1)；
	// t4 空闲恢复；l(k=2) t6,t7；l(k=3) t9 运行一 tick；
	// t10 释放 h(k=1, 需求默认 CL=1) 完成；t11 l(k=3) 完成。
	want := "hhhh..ll.lhl"
	if got != want {
		t.Fatalf("trace=%q want %q", got, want)
	}
	st := m.Stats()
	if st.Switches != 1 || st.Recoveries != 1 || st.Discarded != 1 ||
		st.Skipped != 1 || st.MissedLO != 0 || st.MissedHI != 0 ||
		st.Completions != 4 {
		t.Fatalf("stats=%+v", st)
	}
	// 该作业释放时刻(=3)现已早于当前时刻，再改需求必须被拒。
	if err := m.SetDemand("l", 1, 1); !errors.Is(err, ErrJobReleased) {
		t.Fatalf("re-set skipped job: %v", err)
	}
}

func TestRejectReasons(t *testing.T) {
	valid := Task{ID: "a", Lvl: HI, CL: 1, CH: 1, T: 1, Prio: 1, Phi: 0}

	m := New()
	cases := []struct {
		name string
		task Task
		want error
	}{
		{"empty id", Task{ID: "", Lvl: HI, CL: 1, CH: 1, T: 1, Prio: 1}, ErrInvalidParam},
		{"id too long", Task{ID: "123456789012345678901234567890123", Lvl: HI, CL: 1, CH: 1, T: 1, Prio: 1}, ErrInvalidParam},
		{"CL>CH", Task{ID: "x", Lvl: HI, CL: 2, CH: 1, T: 3, Prio: 1}, ErrInvalidParam},
		{"LO CH!=CL", Task{ID: "x", Lvl: LO, CL: 2, CH: 3, T: 3, Prio: 1}, ErrInvalidParam},
		{"CH>T", Task{ID: "x", Lvl: HI, CL: 1, CH: 2, T: 1, Prio: 1}, ErrInvalidParam},
		{"T=0", Task{ID: "x", Lvl: HI, CL: 1, CH: 1, T: 0, Prio: 1}, ErrInvalidParam},
		{"T>1000", Task{ID: "x", Lvl: HI, CL: 1, CH: 1, T: 1001, Prio: 1}, ErrInvalidParam},
		{"prio=0", Task{ID: "x", Lvl: HI, CL: 1, CH: 1, T: 1, Prio: 0}, ErrInvalidParam},
		{"phi<0", Task{ID: "x", Lvl: HI, CL: 1, CH: 1, T: 1, Prio: 1, Phi: -1}, ErrInvalidParam},
		{"phi>1e6", Task{ID: "x", Lvl: HI, CL: 1, CH: 1, T: 1, Prio: 1, Phi: 1_000_001}, ErrInvalidParam},
	}
	for _, c := range cases {
		if err := m.AddTask(c.task); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.want)
		}
	}
	if err := m.AddTask(valid); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := m.AddTask(valid); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("dup id: %v", err)
	}
	if err := m.AddTask(Task{ID: "b", Lvl: LO, CL: 1, CH: 1, T: 1, Prio: 1}); !errors.Is(err, ErrDuplicatePrio) {
		t.Fatalf("dup prio: %v", err)
	}

	full := New()
	for i := 1; i <= 16; i++ {
		if err := full.AddTask(Task{
			ID: fmt.Sprintf("t%02d", i), Lvl: LO, CL: 1, CH: 1, T: 1, Prio: i,
		}); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if err := full.AddTask(Task{ID: "x", Lvl: LO, CL: 1, CH: 1, T: 1, Prio: 100}); !errors.Is(err, ErrTaskTableFull) {
		t.Fatalf("full: %v", err)
	}

	if err := m.Step(1); err != nil {
		t.Fatalf("Step: %v", err)
	}
	// 开始后：即使参数也非法，也只报 ErrStarted（最先检查）。
	if err := m.AddTask(Task{ID: "zzz", Lvl: LO}); !errors.Is(err, ErrStarted) {
		t.Fatalf("after start: %v", err)
	}

	if err := m.SetDemand("nope", 0, 1); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("unknown: %v", err)
	}
	if err := m.SetDemand("a", -1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("k<0: %v", err)
	}
	if err := m.SetDemand("a", 0, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("c<1: %v", err)
	}
	if err := m.SetDemand("a", 0, 99); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("c>CH: %v", err)
	}
	if err := m.SetDemand("a", 0, 1); !errors.Is(err, ErrJobReleased) {
		t.Fatalf("released: %v", err)
	}
	if err := m.Step(0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Step(0): %v", err)
	}
	if err := m.Step(1_000_001); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Step(1000001): %v", err)
	}

	// 被拒绝的操作不得改变任何状态：此前仅 Step(1)（a 在 tick0 完成）。
	if got, want := m.Stats(), (Stats{Completions: 1}); got != want {
		t.Fatalf("state changed by rejected ops: got=%+v want=%+v", got, want)
	}
	if m.Now() != 1 {
		t.Fatalf("now=%d want 1", m.Now())
	}
}

func TestSetDemandLastWins(t *testing.T) {
	m := New()
	mustAdd(t, m, Task{ID: "h", Lvl: HI, CL: 2, CH: 5, T: 10, Prio: 1, Phi: 0})
	_ = m.SetDemand("h", 0, 5)
	_ = m.SetDemand("h", 0, 2) // 最后一次为准：恰 CL，不切换
	if err := m.Step(2); err != nil {
		t.Fatalf("Step: %v", err)
	}
	if m.Stats().Switches != 0 || m.Mode() != ModeLO {
		t.Fatalf("stats=%+v mode=%v", m.Stats(), m.Mode())
	}
}

func TestStepSplitEquivalence(t *testing.T) {
	build := func() *Monitor {
		m := New()
		mustAdd(t, m, Task{ID: "h", Lvl: HI, CL: 2, CH: 5, T: 10, Prio: 1, Phi: 0})
		mustAdd(t, m, Task{ID: "l", Lvl: LO, CL: 2, CH: 2, T: 4, Prio: 2, Phi: 0})
		_ = m.SetDemand("h", 0, 5)
		return m
	}
	one := build()
	if err := one.Step(12); err != nil {
		t.Fatal(err)
	}
	two := build()
	for _, n := range []int{3, 4, 5} {
		if err := two.Step(n); err != nil {
			t.Fatal(err)
		}
	}
	if traceString(one, 12) != traceString(two, 12) {
		t.Fatalf("trace differs: %q vs %q", traceString(one, 12), traceString(two, 12))
	}
	if one.Stats() != two.Stats() || one.Mode() != two.Mode() {
		t.Fatalf("state differs: %+v/%v vs %+v/%v",
			one.Stats(), one.Mode(), two.Stats(), two.Mode())
	}
}

func TestConcurrentQueries(t *testing.T) {
	m := New()
	mustAdd(t, m, Task{ID: "h", Lvl: HI, CL: 2, CH: 5, T: 10, Prio: 1, Phi: 0})
	mustAdd(t, m, Task{ID: "l", Lvl: LO, CL: 2, CH: 2, T: 4, Prio: 2, Phi: 0})
	_ = m.SetDemand("h", 0, 5)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = m.Mode()
					_ = m.Stats()
					_, _ = m.RunAt(7)
					_ = m.Now()
				}
			}
		}()
	}
	for i := 0; i < 30; i++ {
		if err := m.Step(1); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}
