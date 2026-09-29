package approval_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ontology/approval"
)

var t0 = time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)

// stage 构造一个阶段，截止为 t0+1h。
func stage(name string, k int, approvers ...string) approval.StageSpec {
	return approval.StageSpec{Name: name, Approvers: approvers, K: k, Deadline: t0.Add(time.Hour)}
}

// logOp 打印一次操作的输入与输出。
func logOp(t *testing.T, input string, err error) {
	t.Helper()
	t.Logf("输入: %s -> 输出: err=%v", input, err)
}

// logEvents 打印流程的事件日志（含判定依据）。
func logEvents(t *testing.T, e *approval.Engine, pid string) {
	t.Helper()
	for _, ev := range e.Events(pid) {
		t.Logf("事件#%d [%s] 流程=%s 触发者=%q 判定依据: %s",
			ev.Seq, ev.Type, ev.ProcessID, ev.Actor, ev.Detail)
	}
}

func mustState(t *testing.T, e *approval.Engine, pid string, want approval.State) {
	t.Helper()
	got, err := e.State(pid)
	if err != nil {
		t.Fatalf("State(%s) 出错: %v", pid, err)
	}
	if got != want {
		t.Fatalf("流程 %s 状态 = %s，期望 %s", pid, got, want)
	}
	t.Logf("判定: 流程 %s 终局/状态 = %s（符合预期）", pid, got)
}

// terminalEvents 返回终局事件个数及终局事件之后是否还有事件。
func terminalEvents(e *approval.Engine, pid string) (count int, eventsAfterTerminal int) {
	terminal := false
	for _, ev := range e.Events(pid) {
		switch ev.Type {
		case approval.EventApproved, approval.EventRejected, approval.EventTimeout, approval.EventWithdrawn:
			count++
			terminal = true
		default:
			if terminal {
				eventsAfterTerminal++
			}
		}
	}
	return count, eventsAfterTerminal
}

// 三人需两票：两票否决后 同意0 + 未表态1 = 1 < k=2，立即驳回。
func TestTwoVetoesRejectImmediately(t *testing.T) {
	e := approval.NewEngine(t0)
	if err := e.CreateProcess("p1", "init", []approval.StageSpec{stage("会签", 2, "a", "b", "c")}); err != nil {
		t.Fatal(err)
	}
	logOp(t, `Vote(p1, "a", 否决)`, e.Vote("p1", "a", false))
	mustState(t, e, "p1", approval.StateActive) // 0同意+2未表态=2 仍可能达到 k
	logOp(t, `Vote(p1, "b", 否决)`, e.Vote("p1", "b", false))
	mustState(t, e, "p1", approval.StateRejected) // 0同意+1未表态=1 < 2，提前驳回

	before := len(e.Events("p1"))
	err := e.Vote("p1", "c", true)
	logOp(t, `Vote(p1, "c", 同意)（迟到的表态）`, err)
	if !errors.Is(err, approval.ErrAlreadyTerminal) {
		t.Fatalf("终局后表态应返回 ErrAlreadyTerminal，实际 %v", err)
	}
	if got := len(e.Events("p1")); got != before {
		t.Fatalf("被拒绝的操作产生了事件：之前 %d 条，之后 %d 条", before, got)
	}
	if n, after := terminalEvents(e, "p1"); n != 1 || after != 0 {
		t.Fatalf("终局事件数=%d（期望1），终局后事件数=%d（期望0）", n, after)
	}
	logEvents(t, e, "p1")
}

// 三人需两票：一票否决后两票同意，仍可通过（不是见否决即驳回）。
func TestOneVetoStillPasses(t *testing.T) {
	e := approval.NewEngine(t0)
	if err := e.CreateProcess("p1", "init", []approval.StageSpec{stage("会签", 2, "a", "b", "c")}); err != nil {
		t.Fatal(err)
	}
	logOp(t, `Vote(p1, "a", 否决)`, e.Vote("p1", "a", false))
	mustState(t, e, "p1", approval.StateActive)
	logOp(t, `Vote(p1, "b", 同意)`, e.Vote("p1", "b", true))
	logOp(t, `Vote(p1, "c", 同意)`, e.Vote("p1", "c", true))
	mustState(t, e, "p1", approval.StateApproved)
	logEvents(t, e, "p1")
}

// 同一操作序列串行重放：事件日志与终局完全相同。
func TestSerialReplayDeterminism(t *testing.T) {
	runScript := func() ([]error, []approval.Event, approval.State) {
		e := approval.NewEngine(t0)
		var errs []error
		do := func(err error) { errs = append(errs, err) }
		do(e.CreateProcess("p1", "init", []approval.StageSpec{
			stage("初审", 2, "a", "b", "c"),
			stage("复审", 1, "a", "d"),
		}))
		do(e.Vote("p1", "a", true))
		do(e.Delegate("p1", "b", "x"))
		do(e.Vote("p1", "x", false)) // 代 b 否决：1同意+1未表态=2 仍可达 k
		do(e.Vote("p1", "c", true))  // 初审 2>=2 推进
		do(e.Vote("p1", "a", true))  // a 在复审再次表态
		do(e.Vote("p1", "a", true))  // 重复表态，拒绝
		do(e.AdvanceClock(t0.Add(2 * time.Hour)))
		do(e.Withdraw("p1", "init")) // 已终局，拒绝
		st, err := e.State("p1")
		do(err)
		return errs, e.Events("p1"), st
	}
	errs1, events1, state1 := runScript()
	errs2, events2, state2 := runScript()
	if fmt.Sprint(errs1) != fmt.Sprint(errs2) {
		t.Fatalf("两次重放错误序列不同:\n%v\n%v", errs1, errs2)
	}
	if state1 != state2 {
		t.Fatalf("两次重放终局不同: %s vs %s", state1, state2)
	}
	if fmt.Sprint(events1) != fmt.Sprint(events2) {
		t.Fatalf("两次重放事件日志不同:\n%v\n%v", events1, events2)
	}
	if state1 != approval.StateApproved {
		t.Fatalf("脚本终局 = %s，期望 通过", state1)
	}
	t.Logf("两次串行重放：错误序列、事件日志（%d 条）、终局（%s）完全一致", len(events1), state1)
	for _, ev := range events1 {
		t.Logf("事件#%d [%s] 触发者=%q 判定依据: %s", ev.Seq, ev.Type, ev.Actor, ev.Detail)
	}
}

// 并发压力：表态、委托、撤回、时钟推进任意交错，每个流程恰好一个终局，终局后无事件。
func TestConcurrentExactlyOneTerminal(t *testing.T) {
	const procs = 32
	e := approval.NewEngine(t0)
	for i := 0; i < procs; i++ {
		pid := fmt.Sprintf("p%d", i)
		if err := e.CreateProcess(pid, "init", []approval.StageSpec{
			stage("初审", 2, "a", "b", "c"),
			stage("复审", 2, "a", "d", "e"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	spawn := func(f func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			f()
		}()
	}
	for i := 0; i < procs; i++ {
		pid := fmt.Sprintf("p%d", i)
		for _, v := range []struct {
			who     string
			approve bool
		}{{"a", true}, {"b", false}, {"c", true}, {"d", true}, {"e", false}, {"x", true}} {
			v := v
			spawn(func() { _ = e.Vote(pid, v.who, v.approve) })
		}
		spawn(func() { _ = e.Delegate(pid, "a", "x") })
		spawn(func() { _ = e.Delegate(pid, "b", "y") })
		spawn(func() { _ = e.Withdraw(pid, "init") })
		spawn(func() { _ = e.Withdraw(pid, "not-init") })
		spawn(func() { _ = e.AdvanceClock(t0.Add(30 * time.Minute)) })
		spawn(func() { _ = e.AdvanceClock(t0.Add(2 * time.Hour)) })
	}
	close(start)
	wg.Wait()

	for i := 0; i < procs; i++ {
		pid := fmt.Sprintf("p%d", i)
		st, err := e.State(pid)
		if err != nil {
			t.Fatal(err)
		}
		if !st.Terminal() {
			t.Fatalf("流程 %s 未终局：%s", pid, st)
		}
		if n, after := terminalEvents(e, pid); n != 1 || after != 0 {
			t.Fatalf("流程 %s 终局事件数=%d（期望1），终局后事件数=%d（期望0）", pid, n, after)
		}
	}
	t.Logf("%d 个流程在并发交错下均恰好得到一个终局，终局后无事件", procs)
	logEvents(t, e, "p0")
}

// 委托：受托人的票计作委托人的一票；各类非法委托按优先级拒绝且不改状态。
func TestDelegationAndRedelegation(t *testing.T) {
	e := approval.NewEngine(t0)
	if err := e.CreateProcess("p1", "init", []approval.StageSpec{stage("会签", 2, "a", "b", "c")}); err != nil {
		t.Fatal(err)
	}
	logOp(t, `Delegate(p1, "a", "x")`, e.Delegate("p1", "a", "x"))

	rejected := []struct {
		input string
		err   error
		want  error
	}{
		{`Delegate(p1, "nobody", "y")`, e.Delegate("p1", "nobody", "y"), approval.ErrNotStageMember},
		{`Delegate(p1, "b", "b")（委托给自己）`, e.Delegate("p1", "b", "b"), approval.ErrDelegateToSelf},
		{`Delegate(p1, "b", "c")（委托给本阶段成员）`, e.Delegate("p1", "b", "c"), approval.ErrDelegateTargetInvalid},
		{`Delegate(p1, "b", "x")（委托给已受托者）`, e.Delegate("p1", "b", "x"), approval.ErrDelegateTargetInvalid},
		{`Delegate(p1, "x", "y")（受托人再转委托）`, e.Delegate("p1", "x", "y"), approval.ErrTrusteeRedelegate},
		{`Delegate(p1, "a", "y")（重复委托）`, e.Delegate("p1", "a", "y"), approval.ErrAlreadyDelegated},
	}
	for _, tc := range rejected {
		before := len(e.Events("p1"))
		logOp(t, tc.input, tc.err)
		if !errors.Is(tc.err, tc.want) {
			t.Fatalf("%s：期望 %v，实际 %v", tc.input, tc.want, tc.err)
		}
		if got := len(e.Events("p1")); got != before {
			t.Fatalf("%s：被拒绝的操作改变了状态（事件数 %d -> %d）", tc.input, before, got)
		}
	}

	// 受托人 x 表态，计作委托人 a 的一票。
	logOp(t, `Vote(p1, "x", 同意)（受托人代 a 表决）`, e.Vote("p1", "x", true))
	// 已委托者本人表态，拒绝。
	logOp(t, `Vote(p1, "a", 同意)（已委托者本人表态）`, mustErr(t, e.Vote("p1", "a", true), approval.ErrDelegatorVoted))
	// 受托人重复表态，拒绝。
	logOp(t, `Vote(p1, "x", 同意)（重复表态）`, mustErr(t, e.Vote("p1", "x", true), approval.ErrDuplicateVote))
	// 已表态后再委托，拒绝。
	logOp(t, `Delegate(p1, "a", "z")（已表态后再委托）`, mustErr(t, e.Delegate("p1", "a", "z"), approval.ErrDelegateAfterVote))

	logOp(t, `Vote(p1, "b", 同意)`, e.Vote("p1", "b", true))
	mustState(t, e, "p1", approval.StateApproved)
	logEvents(t, e, "p1")
}

func mustErr(t *testing.T, got, want error) error {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("期望错误 %v，实际 %v", want, got)
	}
	return got
}

// 错误优先级：多个原因同时成立时只报按顺序的第一个。
func TestErrorPriority(t *testing.T) {
	e := approval.NewEngine(t0)
	if err := e.CreateProcess("p1", "init", []approval.StageSpec{stage("会签", 2, "a", "b", "c")}); err != nil {
		t.Fatal(err)
	}
	// 流程不存在优先于一切。
	logOp(t, `Vote("ghost", "a", 同意)`, mustErr(t, e.Vote("ghost", "a", true), approval.ErrProcessNotFound))
	logOp(t, `Delegate("ghost", "a", "x")`, mustErr(t, e.Delegate("ghost", "a", "x"), approval.ErrProcessNotFound))

	// 已终局优先于“非审批人”。
	logOp(t, `Vote(p1, "a", 同意)`, e.Vote("p1", "a", true))
	logOp(t, `Vote(p1, "b", 同意)`, e.Vote("p1", "b", true))
	mustState(t, e, "p1", approval.StateApproved)
	logOp(t, `Vote(p1, "nobody", 同意)（终局+非审批人）`, mustErr(t, e.Vote("p1", "nobody", true), approval.ErrAlreadyTerminal))
	logOp(t, `Delegate(p1, "nobody", "x")（终局+非审批人）`, mustErr(t, e.Delegate("p1", "nobody", "x"), approval.ErrAlreadyTerminal))

	// 非审批人优先于“委托给自己”。
	e2 := approval.NewEngine(t0)
	if err := e2.CreateProcess("p2", "init", []approval.StageSpec{stage("会签", 1, "a")}); err != nil {
		t.Fatal(err)
	}
	logOp(t, `Delegate(p2, "nobody", "nobody")（非审批人+委托给自己）`,
		mustErr(t, e2.Delegate("p2", "nobody", "nobody"), approval.ErrNotStageMember))

	// 受托人转委托给自己：报“委托给自己”而非“受托人再转委托”。
	logOp(t, `Delegate(p2, "a", "x")`, e2.Delegate("p2", "a", "x"))
	logOp(t, `Delegate(p2, "x", "x")（受托人+委托给自己）`,
		mustErr(t, e2.Delegate("p2", "x", "x"), approval.ErrDelegateToSelf))
	logOp(t, `Delegate(p2, "x", "y")（受托人再转委托）`,
		mustErr(t, e2.Delegate("p2", "x", "y"), approval.ErrTrusteeRedelegate))
	logEvents(t, e2, "p2")
}

// 截止边界：表态时刻 < 截止 才有效；恰在截止时流程已超时，表态被拒绝。
func TestVoteExactlyAtDeadline(t *testing.T) {
	deadline := t0.Add(time.Hour)
	e := approval.NewEngine(t0)
	if err := e.CreateProcess("p1", "init", []approval.StageSpec{
		{Name: "会签", Approvers: []string{"a", "b"}, K: 2, Deadline: deadline},
	}); err != nil {
		t.Fatal(err)
	}
	// 截止前 1ns：有效。
	if err := e.AdvanceClock(deadline.Add(-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	logOp(t, `Vote(p1, "a", 同意)（截止前 1ns）`, e.Vote("p1", "a", true))
	mustState(t, e, "p1", approval.StateActive)

	// 时钟到达截止：未达到 k=2，判定超时。
	logOp(t, "AdvanceClock(截止)", e.AdvanceClock(deadline))
	mustState(t, e, "p1", approval.StateTimeout)

	// 恰在截止时刻的表态：流程已终局，被拒绝且不改状态。
	before := len(e.Events("p1"))
	logOp(t, `Vote(p1, "b", 同意)（恰在截止）`, mustErr(t, e.Vote("p1", "b", true), approval.ErrAlreadyTerminal))
	if got := len(e.Events("p1")); got != before {
		t.Fatalf("恰在截止的表态改变了状态（事件数 %d -> %d）", before, got)
	}
	logEvents(t, e, "p1")
}

// 截止前恰好凑齐 k：末阶段通过，时钟越过截止后不再超时。
func TestCompleteJustBeforeDeadline(t *testing.T) {
	deadline := t0.Add(time.Hour)
	e := approval.NewEngine(t0)
	if err := e.CreateProcess("p1", "init", []approval.StageSpec{
		{Name: "会签", Approvers: []string{"a", "b"}, K: 2, Deadline: deadline},
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.AdvanceClock(deadline.Add(-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	logOp(t, `Vote(p1, "a", 同意)`, e.Vote("p1", "a", true))
	logOp(t, `Vote(p1, "b", 同意)`, e.Vote("p1", "b", true))
	mustState(t, e, "p1", approval.StateApproved)
	logOp(t, "AdvanceClock(截止+1h)", e.AdvanceClock(deadline.Add(time.Hour)))
	mustState(t, e, "p1", approval.StateApproved) // 终局不被时钟改变
	if n, after := terminalEvents(e, "p1"); n != 1 || after != 0 {
		t.Fatalf("终局事件数=%d（期望1），终局后事件数=%d（期望0）", n, after)
	}
	logEvents(t, e, "p1")
}

// 撤回与最后一票同时到达：任意交错下恰好一个终局，终局后无事件。
func TestWithdrawRacesLastVote(t *testing.T) {
	for i := 0; i < 200; i++ {
		e := approval.NewEngine(t0)
		pid := fmt.Sprintf("p%d", i)
		if err := e.CreateProcess(pid, "init", []approval.StageSpec{stage("会签", 2, "a", "b")}); err != nil {
			t.Fatal(err)
		}
		if err := e.Vote(pid, "a", true); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		var voteErr, withdrawErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			voteErr = e.Vote(pid, "b", true) // 最后一票：若先生效则通过
		}()
		go func() {
			defer wg.Done()
			<-start
			withdrawErr = e.Withdraw(pid, "init") // 若先生效则撤回
		}()
		close(start)
		wg.Wait()

		state, err := e.State(pid)
		if err != nil {
			t.Fatal(err)
		}
		switch state {
		case approval.StateApproved:
			if voteErr != nil || !errors.Is(withdrawErr, approval.ErrAlreadyTerminal) {
				t.Fatalf(" iter %d: 通过时 voteErr=%v withdrawErr=%v", i, voteErr, withdrawErr)
			}
		case approval.StateWithdrawn:
			if withdrawErr != nil || !errors.Is(voteErr, approval.ErrAlreadyTerminal) {
				t.Fatalf("iter %d: 撤回时 voteErr=%v withdrawErr=%v", i, voteErr, withdrawErr)
			}
		default:
			t.Fatalf("iter %d: 意外终局 %s", i, state)
		}
		if n, after := terminalEvents(e, pid); n != 1 || after != 0 {
			t.Fatalf("iter %d: 终局事件数=%d（期望1），终局后事件数=%d（期望0）", i, n, after)
		}
		if i == 0 {
			t.Logf("输入: Vote(b, 同意) 与 Withdraw(init) 并发 -> 输出: voteErr=%v withdrawErr=%v 终局=%s",
				voteErr, withdrawErr, state)
			logEvents(t, e, pid)
		}
	}
}

// 同一审批人出现在多个阶段：表态只作用于当前阶段。
func TestSameApproverAcrossStages(t *testing.T) {
	e := approval.NewEngine(t0)
	if err := e.CreateProcess("p1", "init", []approval.StageSpec{
		{Name: "初审", Approvers: []string{"a", "b"}, K: 2, Deadline: t0.Add(time.Hour)},
		{Name: "复审", Approvers: []string{"a", "c"}, K: 2, Deadline: t0.Add(2 * time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	logOp(t, `Vote(p1, "c", 同意)（非当前阶段审批人）`, mustErr(t, e.Vote("p1", "c", true), approval.ErrNotStageMember))
	logOp(t, `Vote(p1, "a", 同意)（初审）`, e.Vote("p1", "a", true))
	logOp(t, `Vote(p1, "b", 同意)（初审）`, e.Vote("p1", "b", true))
	// 初审通过，进入复审；a 在复审可再次表态（每阶段至多一次）。
	logOp(t, `Vote(p1, "a", 同意)（复审，同人再表态）`, e.Vote("p1", "a", true))
	logOp(t, `Vote(p1, "b", 同意)（b 不在复审阶段）`, mustErr(t, e.Vote("p1", "b", true), approval.ErrNotStageMember))
	logOp(t, `Vote(p1, "a", 同意)（复审重复表态）`, mustErr(t, e.Vote("p1", "a", true), approval.ErrDuplicateVote))
	logOp(t, `Vote(p1, "c", 同意)（复审）`, e.Vote("p1", "c", true))
	mustState(t, e, "p1", approval.StateApproved)
	logEvents(t, e, "p1")
}
