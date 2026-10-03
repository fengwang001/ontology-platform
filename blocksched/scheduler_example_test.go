package blocksched_test

import (
	"errors"
	"testing"

	"ontology/blocksched"
)

func must(t *testing.T, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func nextBlock(t *testing.T, s *blocksched.Scheduler, now int64, id string) int {
	t.Helper()
	b, ok, err := s.Next(now, id)
	if err != nil || !ok {
		t.Fatalf("Next(%d,%q) = %d,%v,%v; want a block", now, id, b, ok, err)
	}
	return b
}

func nextNone(t *testing.T, s *blocksched.Scheduler, now int64, id string) {
	t.Helper()
	b, ok, err := s.Next(now, id)
	if err != nil || ok {
		t.Fatalf("Next(%d,%q) = %d,%v,%v; want none", now, id, b, ok, err)
	}
}

// TestSpecExample 复现题目给出的完整示例。
func TestSpecExample(t *testing.T) {
	s := blocksched.New(blocksched.Config{
		Blocks: 4, BaseConcurrent: 2, GlobalLimit: 3,
		MaxBlockDup: 2, Timeout: 10, BanThreshold: 2,
	})
	must(t, s.AddPeer("p1", []bool{true, true, true, true}), nil)
	must(t, s.AddPeer("p2", []bool{true, true, false, false}), nil)

	// avail = (2,2,1,1)，最稀有块 2/3 优先。
	if b := nextBlock(t, s, 0, "p1"); b != 2 {
		t.Fatalf("p1 first = %d, want 2", b)
	}
	if b := nextBlock(t, s, 0, "p2"); b != 0 {
		t.Fatalf("p2 first = %d, want 0", b)
	}
	if b := nextBlock(t, s, 0, "p1"); b != 3 {
		t.Fatalf("p1 second = %d, want 3", b)
	}
	// 全局在途已达 G=3。
	nextNone(t, s, 0, "p2")

	// Tick(10)：三条请求 issued=0，now-issued=10 >= T=10，全部过期。
	exp, err := s.Tick(10)
	must(t, err, nil)
	wantExp := []blocksched.Timeout{
		{Issued: 0, Peer: "p1", Block: 2},
		{Issued: 0, Peer: "p1", Block: 3},
		{Issued: 0, Peer: "p2", Block: 0},
	}
	if len(exp) != 3 {
		t.Fatalf("expired = %+v, want 3 entries", exp)
	}
	for i := range wantExp {
		if exp[i] != wantExp[i] {
			t.Fatalf("expired[%d] = %+v, want %+v", i, exp[i], wantExp[i])
		}
	}

	// p1 timeouts=2 -> cap=max(1,2-1)=1；p2 timeouts=1 -> cap=2。
	// Tick 后所有块在途清零，全部重新成为“新块”；稀有度 2、3 并列（avail=1），
	// 取最小编号 -> 2。
	if b := nextBlock(t, s, 10, "p1"); b != 2 {
		t.Fatalf("p1 after tick = %d, want 2", b)
	}
	// p1 cap 已用完，即使全局还有名额也不能再发。
	nextNone(t, s, 10, "p1")

	// p2 只拥有 0、1，块 0 无在途、avail=2，取小编号 0。
	if b := nextBlock(t, s, 10, "p2"); b != 0 {
		t.Fatalf("p2 after tick = %d, want 0", b)
	}
	// 新块仍存在（块 1 在途为 0），p2 取块 1；p2 cap=2 未被降额。
	if b := nextBlock(t, s, 10, "p2"); b != 1 {
		t.Fatalf("p2 second after tick = %d, want 1", b)
	}
	// 全局此时 3 条满。
	nextNone(t, s, 10, "p2")

	// 成功完成块 2：p1 timeouts 清零；该块无其他在途。
	canceled, banned, err := s.Done(10, "p1", 2, true)
	must(t, err, nil)
	if banned || len(canceled) != 0 {
		t.Fatalf("Done p1/2 = %v,%v; want none", canceled, banned)
	}
	// timeouts 清零后 p1 的 cap 恢复为 2；块 3 是唯一在途为 0 的新块，avail=1。
	if b := nextBlock(t, s, 10, "p1"); b != 3 {
		t.Fatalf("p1 after success = %d, want 3", b)
	}
}

// TestEndgameOrder 收尾期选择次序：在途数优先于稀有度。
func TestEndgameOrder(t *testing.T) {
	// B=3, K/G 足够，M=3，T 很大，F=2。
	s := blocksched.New(blocksched.Config{
		Blocks: 3, BaseConcurrent: 4, GlobalLimit: 16,
		MaxBlockDup: 3, Timeout: 1000, BanThreshold: 2,
	})
	// 块 0 稀有（只有 p1），块 1、2 普通。
	must(t, s.AddPeer("p1", []bool{true, true, true}), nil)
	must(t, s.AddPeer("p2", []bool{false, true, true}), nil)
	must(t, s.AddPeer("p3", []bool{false, true, true}), nil)

	// 先把每个未完成块都变成“有在途”，从而进入收尾期：
	// p1 拿 0（avail=1 最稀有），p2 拿 1，p3 拿 2。
	if b := nextBlock(t, s, 0, "p1"); b != 0 {
		t.Fatalf("got %d, want 0", b)
	}
	if b := nextBlock(t, s, 0, "p2"); b != 1 {
		t.Fatalf("got %d, want 1", b)
	}
	if b := nextBlock(t, s, 0, "p3"); b != 2 {
		t.Fatalf("got %d, want 2", b)
	}

	// 现在不存在“新块”。p1 可重复请求 1 或 2：
	// 两块在途数都为 1、avail 都为 3，取小编号 -> 1。
	if b := nextBlock(t, s, 0, "p1"); b != 1 {
		t.Fatalf("endgame tie broken by block number: got %d, want 1", b)
	}

	// 让块 2 在途数仍为 1，块 1 在途数变为 2：p2 重复请求块 1 不合法
	// （同一 (peer,block) 至多一条），改用 p3 对块 1 再请求。
	if b := nextBlock(t, s, 0, "p3"); b != 1 {
		t.Fatalf("p3 endgame = %d, want 1 (flight before rarity)", b)
	}
	// p2 此时块 1 在途=2、块 2 在途=1：尽管块 1 与块 2 avail 相同，
	// 必须优先在途数更小的块 2。
	if b := nextBlock(t, s, 0, "p2"); b != 2 {
		t.Fatalf("flight-count priority: got %d, want 2", b)
	}
}

// TestNewBlockExistsButNoneForPeer 存在新块但该对端没有新块可请求时返回空。
func TestNewBlockExistsButNoneForPeer(t *testing.T) {
	s := blocksched.New(blocksched.Config{
		Blocks: 2, BaseConcurrent: 4, GlobalLimit: 16,
		MaxBlockDup: 2, Timeout: 1000, BanThreshold: 2,
	})
	must(t, s.AddPeer("p1", []bool{true, false}), nil)
	must(t, s.AddPeer("p2", []bool{false, true}), nil)

	// p1 请求块 0 后，块 0 有在途；块 1 是“新块”（avail=1）。
	if b := nextBlock(t, s, 0, "p1"); b != 0 {
		t.Fatalf("got %d, want 0", b)
	}
	// 仍存在新块（块 1），但 p1 不拥有它：即使它可以对块 0 发起收尾重复请求，
	// 也必须返回空。
	nextNone(t, s, 0, "p1")

	// p2 拥有块 1，可以正常拿到。
	if b := nextBlock(t, s, 0, "p2"); b != 1 {
		t.Fatalf("got %d, want 1", b)
	}
}
