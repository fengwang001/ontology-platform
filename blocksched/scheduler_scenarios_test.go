package blocksched_test

import (
	"testing"

	"ontology/blocksched"
)

// TestTimeoutCapAndSuccessReset：超时累计使 cap 下降，成功后清零。
func TestTimeoutCapAndSuccessReset(t *testing.T) {
	s := blocksched.New(blocksched.Config{
		Blocks: 8, BaseConcurrent: 4, GlobalLimit: 64,
		MaxBlockDup: 4, Timeout: 5, BanThreshold: 9,
	})
	must(t, s.AddPeer("p1", []bool{true, true, true, true, true, true, true, true}), nil)

	// 发起 4 条请求占满 K=4。
	for i := 0; i < 4; i++ {
		nextBlock(t, s, 0, "p1")
	}
	nextNone(t, s, 0, "p1")

	// Tick(5)：4 条全过期，timeouts=4，cap=4-2=2。
	exp, err := s.Tick(5)
	must(t, err, nil)
	if len(exp) != 4 {
		t.Fatalf("expired %d, want 4", len(exp))
	}
	for i := 0; i < 2; i++ {
		nextBlock(t, s, 5, "p1")
	}
	nextNone(t, s, 5, "p1") // cap=2 已满

	// 两块按编号 0、1 发放；块 1 成功后 timeouts 清零，cap 恢复 4。
	_, _, err = s.Done(5, "p1", 1, true)
	must(t, err, nil)
	// 当前在途 1 条（块 0），还可再发 3 条到 4 条满。
	for i := 0; i < 3; i++ {
		nextBlock(t, s, 5, "p1")
	}
	nextNone(t, s, 5, "p1")
}

// TestFailBlockOnlyByOthers：校验失败后该对端永不再请求同块，但他人可以。
func TestFailBlockOnlyByOthers(t *testing.T) {
	s := blocksched.New(blocksched.Config{
		Blocks: 1, BaseConcurrent: 4, GlobalLimit: 16,
		MaxBlockDup: 4, Timeout: 1000, BanThreshold: 9,
	})
	must(t, s.AddPeer("p1", []bool{true}), nil)
	must(t, s.AddPeer("p2", []bool{true}), nil)

	if b := nextBlock(t, s, 0, "p1"); b != 0 {
		t.Fatalf("got %d, want 0", b)
	}
	_, banned, err := s.Done(0, "p1", 0, false)
	must(t, err, nil)
	if banned {
		t.Fatal("unexpected ban")
	}
	// 块 0 在途为 0 且 avail=2，仍是“新块”；p1 对它有失败记录 -> 空返回。
	nextNone(t, s, 0, "p1")
	// p2 无失败记录，可请求块 0。
	if b := nextBlock(t, s, 0, "p2"); b != 0 {
		t.Fatalf("p2 got %d, want 0", b)
	}
}

// TestBanChangesAvailAndSelection：封禁后 avail 下降，选择随之改变。
func TestBanChangesAvailAndSelection(t *testing.T) {
	s := blocksched.New(blocksched.Config{
		Blocks: 3, BaseConcurrent: 8, GlobalLimit: 64,
		MaxBlockDup: 4, Timeout: 1000, BanThreshold: 1,
	})
	// p1 只拥有块 0；p2 拥有全部。因此块 1、2 更稀有（avail=1）。
	must(t, s.AddPeer("p1", []bool{true, false, false}), nil)
	must(t, s.AddPeer("p2", []bool{true, true, true}), nil)

	// p2 最先选最稀有的块（1、2 并列，取小编号 1）。
	if b := nextBlock(t, s, 0, "p2"); b != 1 {
		t.Fatalf("got %d, want rarest 1", b)
	}
	// p2 对块 1 校验失败，F=1 立即封禁；其在途请求被移除、avail 下降。
	_, banned, err := s.Done(0, "p2", 1, false)
	must(t, err, nil)
	if !banned {
		t.Fatal("want banned")
	}
	_, _, err = s.Next(0, "p2")
	must(t, err, blocksched.ErrBanned)

	// 块 1、2 的 avail 已降为 0，永远无法完成；p1 完成块 0。
	if b := nextBlock(t, s, 0, "p1"); b != 0 {
		t.Fatalf("p1 got %d, want 0", b)
	}
	canceled, _, err := s.Done(0, "p1", 0, true)
	must(t, err, nil)
	if len(canceled) != 0 {
		t.Fatalf("unexpected canceled %v", canceled)
	}
	if s.Complete() {
		t.Fatal("should not be complete: blocks 1,2 have no available peer")
	}

	// 封禁记录保留：Drop 后同名 id 仍不得 AddPeer；Drop 后 Next 报 ErrNoPeer。
	must(t, s.Drop("p2"), nil)
	must(t, s.AddPeer("p2", []bool{true, true, true}), blocksched.ErrBanned)
	_, _, err = s.Next(0, "p2")
	must(t, err, blocksched.ErrNoPeer)
}

// TestSuccessCancelsOthersFreesSlots：成功取消其他在途，释放对端与全局名额。
func TestSuccessCancelsOthersFreesSlots(t *testing.T) {
	s := blocksched.New(blocksched.Config{
		Blocks: 2, BaseConcurrent: 2, GlobalLimit: 3,
		MaxBlockDup: 4, Timeout: 1000, BanThreshold: 9,
	})
	must(t, s.AddPeer("p1", []bool{true, true}), nil)
	must(t, s.AddPeer("p2", []bool{true, true}), nil)
	must(t, s.AddPeer("p3", []bool{true, true}), nil)

	// 块 0 稀有？avail 相同（都为 3），按编号发：
	// p1 拿 0；p2 拿 1；此后无新块（0、1 都有在途），p3 在收尾期重复拿 0。
	if b := nextBlock(t, s, 0, "p1"); b != 0 {
		t.Fatalf("got %d, want 0", b)
	}
	if b := nextBlock(t, s, 0, "p2"); b != 1 {
		t.Fatalf("got %d, want 1", b)
	}
	if b := nextBlock(t, s, 0, "p3"); b != 0 {
		t.Fatalf("got %d, want 0 (endgame dup)", b)
	}
	nextNone(t, s, 0, "p1") // 全局 G=3 已满

	// p1 完成块 0：p3 对块 0 的重复请求被取消，返回升序 id。
	canceled, banned, err := s.Done(0, "p1", 0, true)
	must(t, err, nil)
	if banned || len(canceled) != 1 || canceled[0] != "p3" {
		t.Fatalf("canceled=%v banned=%v, want [p3]", canceled, banned)
	}
	// 被取消的请求再 Done 报 ErrNoRequest。
	_, _, err = s.Done(0, "p3", 0, true)
	must(t, err, blocksched.ErrNoRequest)
	// 全局名额释放：p1 可请求未完成的块 1。
	if b := nextBlock(t, s, 0, "p1"); b != 1 {
		t.Fatalf("got %d, want 1", b)
	}
}

// TestDropFreesSlots：Drop 取消全部在途、释放名额；未封禁同名 id 可重新加入。
func TestDropFreesSlots(t *testing.T) {
	s := blocksched.New(blocksched.Config{
		Blocks: 1, BaseConcurrent: 2, GlobalLimit: 2,
		MaxBlockDup: 4, Timeout: 1000, BanThreshold: 9,
	})
	must(t, s.AddPeer("p1", []bool{true}), nil)
	must(t, s.AddPeer("p2", []bool{true}), nil)
	nextBlock(t, s, 0, "p1")
	nextBlock(t, s, 0, "p2")
	nextNone(t, s, 0, "p1") // G=2 满

	must(t, s.Drop("p2"), nil)
	_, _, err := s.Next(0, "p2")
	must(t, err, blocksched.ErrNoPeer)
	_, _, err = s.Done(0, "p2", 0, true)
	must(t, err, blocksched.ErrNoPeer)
	// p1 对块 0 已有在途，不能对同一块重复；但新的同名 p2 记录可以。
	nextNone(t, s, 0, "p1")
	must(t, s.AddPeer("p2", []bool{true}), nil)
	if b := nextBlock(t, s, 0, "p2"); b != 0 {
		t.Fatalf("readded p2 got %d, want 0", b)
	}
}
