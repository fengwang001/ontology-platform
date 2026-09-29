package approval

import (
	"fmt"
	"sync"
	"testing"
)

type opKind int

const (
	opVote opKind = iota
	opDelegate
	opWithdraw
	opClock
)

type op struct {
	kind      opKind
	flow      string
	actor     string
	vote      Vote
	delegatee string
	atMin     int
}

func replay(ops []op) ([]Event, map[string]Terminal) {
	e := newTestEngine()
	ids := map[string]string{}
	ids["p"] = e.Start("owner", threePersonStage(2, at(60)))
	ids["q"] = e.Start("owner", []Stage{
		{Name: "S1", Approvers: []string{"alice", "bob"}, Required: 1, Deadline: at(30)},
		{Name: "S2", Approvers: []string{"bob", "carol"}, Required: 1, Deadline: at(90)},
	})
	for _, o := range ops {
		switch o.kind {
		case opVote:
			e.Vote(ids[o.flow], o.actor, o.vote, at(o.atMin))
		case opDelegate:
			e.Delegate(ids[o.flow], o.actor, o.delegatee, at(o.atMin))
		case opWithdraw:
			e.Withdraw(ids[o.flow], o.actor, at(o.atMin))
		case opClock:
			e.AdvanceClock(at(o.atMin))
		}
	}
	ends := map[string]Terminal{}
	for k, id := range ids {
		ends[k] = e.Terminal(id)
	}
	return e.Events(), ends
}

// 同一操作序列串行重放两次，事件日志（除时间戳）与终局必须完全一致。
func TestSerialReplayDeterminism(t *testing.T) {
	ops := []op{
		{kind: opDelegate, flow: "p", actor: "alice", delegatee: "dave", atMin: 1},
		{kind: opVote, flow: "p", actor: "zoe", vote: VoteApprove, atMin: 2},
		{kind: opVote, flow: "p", actor: "dave", vote: VoteApprove, atMin: 3},
		{kind: opClock, atMin: 30},
		{kind: opVote, flow: "q", actor: "alice", vote: VoteApprove, atMin: 10},
		{kind: opVote, flow: "p", actor: "alice", vote: VoteApprove, atMin: 4},
		{kind: opDelegate, flow: "q", actor: "bob", delegatee: "heidi", atMin: 40},
		{kind: opVote, flow: "q", actor: "heidi", vote: VoteReject, atMin: 41},
		{kind: opVote, flow: "q", actor: "carol", vote: VoteApprove, atMin: 42},
		{kind: opVote, flow: "p", actor: "bob", vote: VoteApprove, atMin: 5},
		{kind: opClock, atMin: 100},
	}
	log1, end1 := replay(ops)
	log2, end2 := replay(ops)
	if len(log1) != len(log2) {
		t.Fatalf("重放事件数不一致: %d vs %d", len(log1), len(log2))
	}
	for i := range log1 {
		a, b := log1[i], log2[i]
		if a.Seq != b.Seq || a.FlowID != b.FlowID || a.Kind != b.Kind || a.Detail != b.Detail {
			t.Fatalf("第 %d 条事件重放不一致:\n %+v\n %+v", i, a, b)
		}
	}
	if fmt.Sprint(end1) != fmt.Sprint(end2) {
		t.Fatalf("重放终局不一致: %v vs %v", end1, end2)
	}
}

// 高并发随机交错：每个流程始终恰好一个终局，终局事件唯一。
func TestConcurrentInterleavings(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		e := newTestEngine()
		id := e.Start("owner", threePersonStage(2, at(60)))

		var wg sync.WaitGroup
		voters := []string{"alice", "bob", "carol"}
		for _, v := range voters {
			wg.Add(1)
			go func(who string) {
				defer wg.Done()
				e.Vote(id, who, VoteApprove, at(2))
				e.Vote(id, who, VoteApprove, at(3)) // 重复
			}(v)
		}
		wg.Add(3)
		go func() { defer wg.Done(); e.Delegate(id, "alice", "dave", at(1)) }()
		go func() { defer wg.Done(); e.Withdraw(id, "owner", at(2)) }()
		go func() { defer wg.Done(); e.AdvanceClock(at(60)) }()
		wg.Wait()

		term := e.Terminal(id)
		if term == 0 {
			t.Fatalf("迭代%d: 并发结束后必须已有终局", iter)
		}
		count := 0
		for _, ev := range e.Events() {
			if ev.FlowID == id && ev.Kind == "Terminal" {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("迭代%d: 终局事件必须恰好一个，实际=%d", iter, count)
		}
		// 终局后再推进时钟不产生新事件
		before := len(e.Events())
		e.AdvanceClock(at(120))
		e.Vote(id, "alice", VoteApprove, at(120))
		if len(e.Events()) != before {
			t.Fatalf("迭代%d: 终局后不得再产生事件", iter)
		}
	}
}
