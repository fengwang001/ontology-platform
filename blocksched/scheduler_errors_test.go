package blocksched_test

import (
	"errors"
	"testing"

	"ontology/blocksched"
)

// TestErrorPriorities 校验各入口的错误优先级与时钟行为。
func TestErrorPriorities(t *testing.T) {
	s := blocksched.New(blocksched.Config{
		Blocks: 2, BaseConcurrent: 2, GlobalLimit: 4,
		MaxBlockDup: 2, Timeout: 10, BanThreshold: 1,
	})

	// AddPeer：ErrBadArg > ErrPeerExists。
	must(t, s.AddPeer("", []bool{true, true}), blocksched.ErrBadArg)
	must(t, s.AddPeer("p1", []bool{true}), blocksched.ErrBadArg)
	must(t, s.AddPeer("p1", []bool{true, true}), nil)
	must(t, s.AddPeer("p1", []bool{true, true}), blocksched.ErrPeerExists)

	// Have：先 ErrNoPeer 再 ErrBadArg。
	must(t, s.Have("nobody", 99), blocksched.ErrNoPeer)
	must(t, s.Have("p1", 99), blocksched.ErrBadArg)
	must(t, s.Have("p1", -1), blocksched.ErrBadArg)

	// Have 对已封禁但未 Drop 的对端仍视为存在（置位但不计 avail）。
	nextBlock(t, s, 0, "p1") // 块 0
	_, banned, err := s.Done(0, "p1", 0, false)
	must(t, err, nil)
	if !banned {
		t.Fatal("want ban with F=1")
	}
	must(t, s.Have("p1", 1), nil)

	// Next：ErrClock > ErrNoPeer > ErrBanned。
	_, _, err = s.Next(-1, "p1")
	must(t, err, blocksched.ErrClock)
	_, _, err = s.Next(-1, "ghost")
	must(t, err, blocksched.ErrClock)
	_, _, err = s.Next(0, "ghost")
	must(t, err, blocksched.ErrNoPeer)
	_, _, err = s.Next(0, "p1")
	must(t, err, blocksched.ErrBanned)

	// Done：ErrClock > ErrNoPeer > ErrBadArg > ErrNoRequest。
	_, _, err = s.Done(-1, "ghost", 99, true)
	must(t, err, blocksched.ErrClock)
	_, _, err = s.Done(0, "ghost", 99, true)
	must(t, err, blocksched.ErrNoPeer)

	s2 := blocksched.New(blocksched.Config{
		Blocks: 2, BaseConcurrent: 2, GlobalLimit: 4,
		MaxBlockDup: 2, Timeout: 10, BanThreshold: 9,
	})
	must(t, s2.AddPeer("p1", []bool{true, true}), nil)
	nextBlock(t, s2, 0, "p1") // 块 0
	_, _, err = s2.Done(0, "p1", 99, true)
	must(t, err, blocksched.ErrBadArg)
	_, _, err = s2.Done(0, "p1", 1, true)
	must(t, err, blocksched.ErrNoRequest)

	// Tick 的 ErrClock；回退调用不改状态。
	_, err = s.Tick(5)
	must(t, err, nil)
	_, err = s.Tick(4)
	must(t, err, blocksched.ErrClock)

	// Drop：不存在报 ErrNoPeer；封禁者 Drop 后封禁记录保留。
	must(t, s.Drop("nobody"), blocksched.ErrNoPeer)
	must(t, s.Drop("p1"), nil)
	must(t, s.Drop("p1"), blocksched.ErrNoPeer)
	must(t, s.AddPeer("p1", nil), blocksched.ErrBadArg) // 长度不对优先

	if !errors.Is(blocksched.ErrBanned, blocksched.ErrBanned) {
		t.Fatal("sentinel identity broken")
	}
}

// TestCompleteThenNextEmpty：全部完成后 Next 恒空，Complete 为真。
func TestCompleteThenNextEmpty(t *testing.T) {
	s := blocksched.New(blocksched.Config{
		Blocks: 1, BaseConcurrent: 2, GlobalLimit: 4,
		MaxBlockDup: 2, Timeout: 1000, BanThreshold: 9,
	})
	must(t, s.AddPeer("p1", []bool{true}), nil)
	must(t, s.AddPeer("p2", []bool{true}), nil)
	if b := nextBlock(t, s, 0, "p1"); b != 0 {
		t.Fatalf("got %d, want 0", b)
	}
	canceled, _, err := s.Done(0, "p1", 0, true)
	must(t, err, nil)
	if len(canceled) != 0 {
		t.Fatalf("unexpected canceled %v", canceled)
	}
	if !s.Complete() {
		t.Fatal("want complete")
	}
	nextNone(t, s, 0, "p1")
	nextNone(t, s, 0, "p2")
	_, err = s.Tick(100)
	must(t, err, nil)
}
