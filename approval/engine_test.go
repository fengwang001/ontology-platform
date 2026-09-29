package approval

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

var base = time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)

func at(min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }

func newTestEngine() *Engine {
	e := NewEngine(fixedClock{t: base})
	e.SetLogger(func(s string) {
		fmt.Println("    " + s)
	})
	return e
}

func threePersonStage(k int, deadline time.Time) []Stage {
	return []Stage{{
		Name:      "部门会签",
		Approvers: []string{"alice", "bob", "carol"},
		Required:  k,
		Deadline:  deadline,
	}}
}

// 场景1：三人需两票，两张否决票后同意数+未表态 < k，立即驳回。
func TestTwoRejectsEarlyReject(t *testing.T) {
	e := newTestEngine()
	id := e.Start("owner", threePersonStage(2, at(60)))

	r1 := e.Vote(id, "alice", VoteReject, at(1))
	r2 := e.Vote(id, "bob", VoteReject, at(2))
	if r1.OK != true || r2.OK != true {
		t.Fatalf("否决表态应当被接受: %+v %+v", r1, r2)
	}
	if got := e.Terminal(id); got != TerminalRejected {
		t.Fatalf("两票否决后应立即驳回，实际=%s", got)
	}
	// 终局后的迟到同意不改变结果且不产生新事件
	n := len(e.Events())
	r3 := e.Vote(id, "carol", VoteApprove, at(3))
	if r3.OK || r3.RejectCode != RejectAlreadyTerminal || len(e.Events()) != n {
		t.Fatalf("终局后表态应被拒绝且不产生事件: %+v events=%d->%d", r3, n, len(e.Events()))
	}
}

// 场景2：一票否决仍可通过（剩余两人均同意即达到 k=2）。
func TestOneRejectStillApprovable(t *testing.T) {
	e := newTestEngine()
	id := e.Start("owner", threePersonStage(2, at(60)))

	if r := e.Vote(id, "alice", VoteReject, at(1)); !r.OK {
		t.Fatalf("否决应被接受: %+v", r)
	}
	if got := e.Terminal(id); got != 0 {
		t.Fatalf("一票否决不应立即终局，实际=%s", got)
	}
	if r := e.Vote(id, "bob", VoteApprove, at(2)); !r.OK {
		t.Fatalf("同意应被接受: %+v", r)
	}
	r := e.Vote(id, "carol", VoteApprove, at(3))
	if !r.OK || r.Terminal != TerminalApproved || e.Terminal(id) != TerminalApproved {
		t.Fatalf("两张同意后应通过，r=%+v terminal=%s", r, e.Terminal(id))
	}
}

// 场景3：委托成立，受托人投票计作委托人一票；委托人本人投票被拒；转委托被拒。
func TestDelegationAndReDelegation(t *testing.T) {
	e := newTestEngine()
	id := e.Start("owner", threePersonStage(2, at(60)))

	if r := e.Delegate(id, "alice", "dave", at(1)); !r.OK {
		t.Fatalf("委托应成立: %+v", r)
	}
	// 受托人再转委托
	if r := e.Delegate(id, "dave", "erin", at(2)); r.OK || r.RejectCode != RejectDelegateeRedelegating {
		t.Fatalf("受托人转委托应被拒（受托人再转委托），实际=%+v", r)
	}
	// 已委托者本人表态
	if r := e.Vote(id, "alice", VoteApprove, at(3)); r.OK || r.RejectCode != RejectDelegatorVoting {
		t.Fatalf("已委托者本人表态应被拒，实际=%+v", r)
	}
	// 受托人表态，计入 alice
	if r := e.Vote(id, "dave", VoteApprove, at(4)); !r.OK {
		t.Fatalf("受托人表态应被接受: %+v", r)
	}
	if r := e.Vote(id, "bob", VoteApprove, at(5)); !r.OK || r.Terminal != TerminalApproved {
		t.Fatalf("第二张同意后应通过: %+v", r)
	}

	// 委托错误优先级的其余分支（新流程）
	id2 := e.Start("owner", threePersonStage(2, at(60)))
	cases := []struct {
		from, to string
		want     RejectCode
	}{
		{"alice", "alice", RejectDelegateToSelf},   // 委托给自己
		{"alice", "bob", RejectDelegateeIsMember},  // 受托人是本阶段成员
		{"stranger", "dave", RejectNotParticipant}, // 既非审批人也非受托人
	}
	for i, c := range cases {
		if r := e.Delegate(id2, c.from, c.to, at(1)); r.OK || r.RejectCode != c.want {
			t.Fatalf("用例%d 期望=%s 实际=%+v", i, c.want, r)
		}
	}
	if r := e.Delegate(id2, "alice", "dave", at(1)); !r.OK {
		t.Fatalf("合法委托失败: %+v", r)
	}
	// 受托人已是别人的受托人
	if r := e.Delegate(id2, "bob", "dave", at(2)); r.OK || r.RejectCode != RejectDelegateeIsMember {
		t.Fatalf("委托给已受托者应被拒: %+v", r)
	}
	// 重复委托
	if r := e.Delegate(id2, "alice", "frank", at(2)); r.OK || r.RejectCode != RejectAlreadyDelegated {
		t.Fatalf("重复委托应被拒: %+v", r)
	}
	// 已表态后再委托
	id3 := e.Start("owner", threePersonStage(2, at(60)))
	if r := e.Vote(id3, "carol", VoteApprove, at(1)); !r.OK {
		t.Fatalf("表态失败: %+v", r)
	}
	if r := e.Delegate(id3, "carol", "grace", at(2)); r.OK || r.RejectCode != RejectVoteAfterDelegation {
		t.Fatalf("已表态后委托应被拒: %+v", r)
	}
}

// 场景4：恰在截止时刻的表态无效（严格小于截止才有效），时钟到截止判超时。
func TestDeadlineBoundary(t *testing.T) {
	e := newTestEngine()
	id := e.Start("owner", threePersonStage(2, at(60)))

	if r := e.Vote(id, "alice", VoteApprove, at(60)); r.OK || r.RejectCode != RejectAlreadyTerminal {
		t.Fatalf("恰在截止的表态应因超时终局被拒，实际=%+v", r)
	}
	if got := e.Terminal(id); got != TerminalTimedOut {
		t.Fatalf("应判超时，实际=%s", got)
	}

	// 截止前一分钟有效
	e2 := newTestEngine()
	id2 := e2.Start("owner", threePersonStage(2, at(60)))
	if r := e2.Vote(id2, "alice", VoteApprove, at(59)); !r.OK {
		t.Fatalf("截止前表态应有效: %+v", r)
	}
	e2.AdvanceClock(at(60))
	if got := e2.Terminal(id2); got != TerminalTimedOut {
		t.Fatalf("时钟到截止且未达 k 应超时，实际=%s", got)
	}
}

// 场景5：撤回与最后一票同时到达——并发交错下只有一个终局。
func TestWithdrawVersusFinalVote(t *testing.T) {
	e := newTestEngine()
	id := e.Start("owner", threePersonStage(2, at(60)))
	if r := e.Vote(id, "alice", VoteApprove, at(1)); !r.OK {
		t.Fatalf("首票失败: %+v", r)
	}

	var wg sync.WaitGroup
	results := make([]Result, 2)
	wg.Add(2)
	go func() { defer wg.Done(); results[0] = e.Vote(id, "bob", VoteApprove, at(2)) }()
	go func() { defer wg.Done(); results[1] = e.Withdraw(id, "owner", at(2)) }()
	wg.Wait()

	term := e.Terminal(id)
	if term != TerminalApproved && term != TerminalWithdrawn {
		t.Fatalf("终局只能是通过或撤回，实际=%s", term)
	}
	accepted := 0
	for _, r := range results {
		if r.OK {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("两个同时操作必须恰好一个生效，实际生效=%d (%+v)", accepted, results)
	}
	terminals := 0
	for _, ev := range e.Events() {
		if ev.Kind == "Terminal" {
			terminals++
		}
	}
	if terminals != 1 {
		t.Fatalf("事件日志中必须恰好一个终局事件，实际=%d", terminals)
	}
}

// 场景6：同一审批人出现在多个阶段，表态只作用于当前阶段。
func TestSameApproverAcrossStages(t *testing.T) {
	e := newTestEngine()
	stages := []Stage{
		{Name: "S1", Approvers: []string{"alice", "bob"}, Required: 1, Deadline: at(60)},
		{Name: "S2", Approvers: []string{"alice", "carol"}, Required: 1, Deadline: at(120)},
	}
	id := e.Start("owner", stages)

	if r := e.Vote(id, "alice", VoteApprove, at(1)); !r.OK || r.Terminal != 0 {
		t.Fatalf("S1 同意后应推进而非通过: %+v", r)
	}
	// bob 在 S2 既非成员也非受托人
	if r := e.Vote(id, "bob", VoteApprove, at(2)); r.OK || r.RejectCode != RejectNotParticipant {
		t.Fatalf("上一阶段成员在新阶段表态应被拒: %+v", r)
	}
	// alice 在 S2 仍可表态（各阶段状态独立，不算重复）
	if r := e.Vote(id, "alice", VoteApprove, at(3)); !r.OK || r.Terminal != TerminalApproved {
		t.Fatalf("S2 alice 同意后应通过: %+v", r)
	}
}

// 重复表态与越权表态不改变结果。
func TestDuplicateAndUnauthorizedVote(t *testing.T) {
	e := newTestEngine()
	id := e.Start("owner", threePersonStage(2, at(60)))

	if r := e.Vote(id, "alice", VoteApprove, at(1)); !r.OK {
		t.Fatalf("首票失败: %+v", r)
	}
	if r := e.Vote(id, "alice", VoteReject, at(2)); r.OK || r.RejectCode != RejectDuplicateVote {
		t.Fatalf("重复表态应被拒: %+v", r)
	}
	if r := e.Vote(id, "zoe", VoteApprove, at(2)); r.OK || r.RejectCode != RejectNotParticipant {
		t.Fatalf("越权表态应被拒: %+v", r)
	}
	if r := e.Vote("no-such-flow", "alice", VoteApprove, at(2)); r.OK || r.RejectCode != RejectFlowNotFound {
		t.Fatalf("不存在流程应报流程不存在: %+v", r)
	}
	// 状态未被污染：bob、carol 同意仍可正常通过
	if r := e.Vote(id, "bob", VoteApprove, at(3)); !r.OK || r.Terminal != TerminalApproved {
		t.Fatalf("被拒操作不应污染状态: %+v", r)
	}
}

// 非发起人撤回被拒；终局后撤回不产生事件。
func TestWithdrawRules(t *testing.T) {
	e := newTestEngine()
	id := e.Start("owner", threePersonStage(2, at(60)))
	if r := e.Withdraw(id, "intruder", at(1)); r.OK || r.RejectCode != RejectNotParticipant {
		t.Fatalf("非发起人撤回应被拒: %+v", r)
	}
	if r := e.Withdraw(id, "owner", at(1)); !r.OK || r.Terminal != TerminalWithdrawn {
		t.Fatalf("发起人撤回失败: %+v", r)
	}
	n := len(e.Events())
	if r := e.Withdraw(id, "owner", at(2)); r.OK || r.RejectCode != RejectAlreadyTerminal || len(e.Events()) != n {
		t.Fatalf("终局后撤回应被拒且不产生事件: %+v", r)
	}
}
