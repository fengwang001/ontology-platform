package committer

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// logState 打印操作、副作用存储、位点与判定依据。
func logState(t *testing.T, c *Committer, op string, err error) {
	t.Helper()
	decision := "接受"
	if err != nil {
		var cerr *Error
		if errors.As(err, &cerr) {
			decision = fmt.Sprintf("拒绝(%s): %s", cerr.Kind, cerr.Detail)
		} else {
			decision = fmt.Sprintf("失败: %v", err)
		}
	}
	t.Logf("op=%s => %s | position=%d effects=%v pending=%v",
		op, decision, c.Position(), dumpEffects(c), c.Pending())
}

// dumpEffects 在锁内读取副作用存储快照用于日志打印。
func dumpEffects(c *Committer) map[int64]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[int64]string, len(c.effects))
	for seq, effect := range c.effects {
		out[seq] = effect
	}
	return out
}

func openTemp(t *testing.T) (*Committer, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "committer.json")
	c, err := Open(path)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	return c, path
}

func reopen(t *testing.T, path string) *Committer {
	t.Helper()
	c, err := Open(path)
	if err != nil {
		t.Fatalf("重启 Open 失败: %v", err)
	}
	return c
}

func mustKind(t *testing.T, err error, kind ErrKind) {
	t.Helper()
	var cerr *Error
	if !errors.As(err, &cerr) || cerr.Kind != kind {
		t.Fatalf("期望错误类型 %s，实际: %v", kind, err)
	}
}

// assertStateUnchanged 校验一次失败操作后副作用存储与位点均未改变。
func assertStateUnchanged(t *testing.T, c *Committer, posBefore int64, effectsBefore map[int64]string) {
	t.Helper()
	if got := c.Position(); got != posBefore {
		t.Fatalf("失败后位点被改变: before=%d after=%d", posBefore, got)
	}
	got := dumpEffects(c)
	if len(got) != len(effectsBefore) {
		t.Fatalf("失败后副作用存储被改变: before=%v after=%v", effectsBefore, got)
	}
	for seq, effect := range effectsBefore {
		if got[seq] != effect {
			t.Fatalf("失败后副作用存储被改变: before=%v after=%v", effectsBefore, got)
		}
	}
}

func TestTwoPhaseHappyPath(t *testing.T) {
	c, _ := openTemp(t)
	for seq := int64(1); seq <= 3; seq++ {
		err := c.WriteEffect(seq, fmt.Sprintf("effect-%d", seq))
		logState(t, c, fmt.Sprintf("WriteEffect(%d)", seq), err)
		if err != nil {
			t.Fatalf("WriteEffect(%d) 失败: %v", seq, err)
		}
		err = c.Commit(seq)
		logState(t, c, fmt.Sprintf("Commit(%d)", seq), err)
		if err != nil {
			t.Fatalf("Commit(%d) 失败: %v", seq, err)
		}
	}
	if got := c.Position(); got != 3 {
		t.Fatalf("位点应为 3，实际 %d", got)
	}
	if err := c.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	if pending := c.Pending(); len(pending) != 0 {
		t.Fatalf("不应有未提交副作用: %v", pending)
	}
}

func TestCrashBetweenPhases(t *testing.T) {
	c, path := openTemp(t)
	logState(t, c, "WriteEffect(1)", c.WriteEffect(1, "effect-1"))
	logState(t, c, "Commit(1)", c.Commit(1))
	// 阶段一完成、阶段二未做时崩溃。
	logState(t, c, "WriteEffect(2)", c.WriteEffect(2, "effect-2"))

	// 模拟崩溃重启：重新打开同一状态文件。
	c2 := reopen(t, path)
	t.Logf("重启后: position=%d effects=%v pending=%v", c2.Position(), dumpEffects(c2), c2.Pending())
	if got := c2.Position(); got != 1 {
		t.Fatalf("崩溃后位点应保留为 1，实际 %d", got)
	}
	pending := c2.Pending()
	if len(pending) != 1 || pending[0] != 2 {
		t.Fatalf("崩溃后应返回待重复处理序号 [2]，实际 %v", pending)
	}
	// 重复处理：幂等重写副作用后提交。
	logState(t, c2, "WriteEffect(2) 重放", c2.WriteEffect(2, "effect-2"))
	if err := c2.Commit(2); err != nil {
		t.Fatalf("重启后 Commit(2) 失败: %v", err)
	}
	logState(t, c2, "Commit(2)", nil)
	if got := c2.Position(); got != 2 {
		t.Fatalf("位点应推进到 2，实际 %d", got)
	}
	if err := c2.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

func TestOutOfOrderWriteRejected(t *testing.T) {
	c, _ := openTemp(t)
	logState(t, c, "WriteEffect(1)", c.WriteEffect(1, "effect-1"))
	logState(t, c, "Commit(1)", c.Commit(1))

	posBefore, effectsBefore := c.Position(), dumpEffects(c)
	err := c.WriteEffect(5, "effect-5")
	logState(t, c, "WriteEffect(5)", err)
	mustKind(t, err, ErrOutOfOrder)
	assertStateUnchanged(t, c, posBefore, effectsBefore)
}

func TestPositionJumpCommitRejected(t *testing.T) {
	c, _ := openTemp(t)
	logState(t, c, "WriteEffect(1)", c.WriteEffect(1, "effect-1"))
	logState(t, c, "Commit(1)", c.Commit(1))

	posBefore, effectsBefore := c.Position(), dumpEffects(c)
	err := c.Commit(3)
	logState(t, c, "Commit(3)", err)
	mustKind(t, err, ErrPositionJump)
	assertStateUnchanged(t, c, posBefore, effectsBefore)
}

func TestMissingEffectCommitRejected(t *testing.T) {
	c, _ := openTemp(t)
	posBefore, effectsBefore := c.Position(), dumpEffects(c)
	err := c.Commit(1)
	logState(t, c, "Commit(1) 无副作用", err)
	mustKind(t, err, ErrMissingEffect)
	assertStateUnchanged(t, c, posBefore, effectsBefore)
}

func TestInvalidSeqRejected(t *testing.T) {
	c, _ := openTemp(t)
	logState(t, c, "WriteEffect(1)", c.WriteEffect(1, "effect-1"))
	logState(t, c, "Commit(1)", c.Commit(1))

	cases := []struct {
		name string
		op   func() error
	}{
		{"WriteEffect(0)", func() error { return c.WriteEffect(0, "x") }},
		{"WriteEffect(-1)", func() error { return c.WriteEffect(-1, "x") }},
		{"WriteEffect(1) 已提交", func() error { return c.WriteEffect(1, "x") }},
		{"Commit(0)", func() error { return c.Commit(0) }},
		{"Commit(1) 已提交", func() error { return c.Commit(1) }},
	}
	for _, tc := range cases {
		posBefore, effectsBefore := c.Position(), dumpEffects(c)
		err := tc.op()
		logState(t, c, tc.name, err)
		mustKind(t, err, ErrInvalidSeq)
		assertStateUnchanged(t, c, posBefore, effectsBefore)
	}
}

func TestDuplicateWriteIdempotent(t *testing.T) {
	c, _ := openTemp(t)
	logState(t, c, "WriteEffect(1)", c.WriteEffect(1, "effect-1"))
	// 同一在途序号重复写入：幂等成功，存储不变。
	err := c.WriteEffect(1, "effect-1")
	logState(t, c, "WriteEffect(1) 重复", err)
	if err != nil {
		t.Fatalf("重复写入应幂等成功: %v", err)
	}
	if got := dumpEffects(c); len(got) != 1 || got[1] != "effect-1" {
		t.Fatalf("重复写入后副作用存储应为 {1: effect-1}，实际 %v", got)
	}
	logState(t, c, "Commit(1)", c.Commit(1))
	// 位点已推进，再次提交同一序号为非法序号。
	posBefore, effectsBefore := c.Position(), dumpEffects(c)
	err = c.Commit(1)
	logState(t, c, "Commit(1) 重复", err)
	mustKind(t, err, ErrInvalidSeq)
	assertStateUnchanged(t, c, posBefore, effectsBefore)
	if got := c.Position(); got != 1 {
		t.Fatalf("位点应恰好推进一次到 1，实际 %d", got)
	}
}

func TestConcurrentWriteAndCommit(t *testing.T) {
	c, _ := openTemp(t)
	const workers = 16
	const rounds = 50
	for round := int64(1); round <= rounds; round++ {
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(seq int64) {
				defer wg.Done()
				// 并发重复写入同一在途序号。
				if err := c.WriteEffect(seq, fmt.Sprintf("effect-%d", seq)); err != nil {
					var cerr *Error
					if !errors.As(err, &cerr) || cerr.Kind != ErrInvalidSeq {
						t.Errorf("并发 WriteEffect(%d) 出现意外错误: %v", seq, err)
					}
				}
				// 并发提交：位点对每个序号恰好推进一次。
				if err := c.Commit(seq); err != nil {
					var cerr *Error
					if !errors.As(err, &cerr) {
						t.Errorf("并发 Commit(%d) 出现意外错误: %v", seq, err)
					}
				}
			}(round)
		}
		wg.Wait()
		if got := c.Position(); got != round {
			t.Fatalf("第 %d 轮后位点应为 %d，实际 %d", round, round, got)
		}
	}
	t.Logf("并发结束: position=%d pending=%v", c.Position(), c.Pending())
	if err := c.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

func TestConcurrentQueryAndCheck(t *testing.T) {
	c, _ := openTemp(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prev := int64(-1)
			for {
				select {
				case <-stop:
					return
				default:
				}
				pos := c.Position()
				if pos < prev {
					t.Errorf("位点回退: %d -> %d", prev, pos)
				}
				prev = pos
				_ = c.Pending()
				_, _ = c.Effect(pos)
				if err := c.Check(); err != nil {
					t.Errorf("并发自检失败: %v", err)
				}
			}
		}()
	}
	for seq := int64(1); seq <= 100; seq++ {
		if err := c.WriteEffect(seq, "e"); err != nil {
			t.Fatalf("WriteEffect(%d) 失败: %v", seq, err)
		}
		if err := c.Commit(seq); err != nil {
			t.Fatalf("Commit(%d) 失败: %v", seq, err)
		}
	}
	close(stop)
	wg.Wait()
}

// op 记录一条可重放的操作，用于崩溃后按日志重放核对结果。
type op struct {
	Kind   string // "write" 或 "commit"
	Seq    int64
	Effect string
}

// replay 在全新提交器上重放操作序列，返回最终位点与副作用存储。
func replay(t *testing.T, path string, ops []op) (int64, map[int64]string) {
	t.Helper()
	c, err := Open(path)
	if err != nil {
		t.Fatalf("重放 Open 失败: %v", err)
	}
	for _, o := range ops {
		switch o.Kind {
		case "write":
			_ = c.WriteEffect(o.Seq, o.Effect)
		case "commit":
			_ = c.Commit(o.Seq)
		}
	}
	return c.Position(), dumpEffects(c)
}

func TestReplayDeterministic(t *testing.T) {
	// 构造一段含合法与非法操作的序列。
	ops := []op{
		{Kind: "write", Seq: 1, Effect: "effect-1"},
		{Kind: "commit", Seq: 1},
		{Kind: "write", Seq: 3, Effect: "effect-3"}, // 越序，拒绝
		{Kind: "commit", Seq: 3},                    // 位点跳跃，拒绝
		{Kind: "write", Seq: 2, Effect: "effect-2"},
		{Kind: "write", Seq: 2, Effect: "effect-2"}, // 幂等重复
		{Kind: "commit", Seq: 2},
		{Kind: "commit", Seq: 2}, // 非法序号，拒绝
		{Kind: "write", Seq: 3, Effect: "effect-3"},
		// 模拟崩溃：序号 3 只写副作用未提交。
	}
	dir := t.TempDir()
	pos1, effects1 := replay(t, filepath.Join(dir, "a.json"), ops)
	pos2, effects2 := replay(t, filepath.Join(dir, "b.json"), ops)
	t.Logf("重放结果: position=%d effects=%v", pos1, effects1)
	if pos1 != pos2 {
		t.Fatalf("两次重放位点不一致: %d vs %d", pos1, pos2)
	}
	if len(effects1) != len(effects2) {
		t.Fatalf("两次重放副作用存储不一致: %v vs %v", effects1, effects2)
	}
	for seq, effect := range effects1 {
		if effects2[seq] != effect {
			t.Fatalf("两次重放副作用存储不一致: %v vs %v", effects1, effects2)
		}
	}
	if pos1 != 2 {
		t.Fatalf("重放后位点应为 2，实际 %d", pos1)
	}
	// 崩溃恢复：重放方重新打开后应看到待处理序号 3。
	c := reopen(t, filepath.Join(dir, "a.json"))
	pending := c.Pending()
	sort.Slice(pending, func(i, j int) bool { return pending[i] < pending[j] })
	if len(pending) != 1 || pending[0] != 3 {
		t.Fatalf("重放后应返回待处理序号 [3]，实际 %v", pending)
	}
}
