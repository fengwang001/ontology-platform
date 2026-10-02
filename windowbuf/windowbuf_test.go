package windowbuf

import (
	"fmt"
	"strings"
	"testing"
)

func mustNew(t *testing.T, s, g, e int64, p Policy) *Buffer {
	t.Helper()
	b, err := New(s, g, e, p)
	if err != nil {
		t.Fatalf("New(%d, %d, %d, %v): %v", s, g, e, p, err)
	}
	return b
}

func emitStr(e Emitted) string {
	return fmt.Sprintf("(%s,%d,%d,%d,%s)", e.Key, e.Ws, e.Value, e.LastTs, e.Mark)
}

func emitsStr(em []Emitted) string {
	parts := make([]string, len(em))
	for i, e := range em {
		parts[i] = emitStr(e)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func entryStr(e Entry) string {
	return fmt.Sprintf("(%s,%d,%d,%d)", e.Key, e.Ws, e.Value, e.LastTs)
}

func bufferedStr(b *Buffer) string {
	parts := make([]string, 0)
	for _, e := range b.Buffered() {
		parts = append(parts, entryStr(e))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// checkUpdate runs one Update, logs input/output/basis, and asserts the
// outcome and the emitted sequence.
func checkUpdate(t *testing.T, b *Buffer, key string, ts, value int64, wantOut Outcome, wantEm string, basis string) {
	t.Helper()
	out, em := b.Update([]byte(key), ts, value)
	t.Logf("Update(%q, %d, %d) -> %s %s | 依据: %s", key, ts, value, out, emitsStr(em), basis)
	if out != wantOut {
		t.Errorf("Update(%q, %d, %d): outcome = %s, want %s", key, ts, value, out, wantOut)
	}
	if got := emitsStr(em); got != wantEm {
		t.Errorf("Update(%q, %d, %d): emitted = %s, want %s", key, ts, value, got, wantEm)
	}
}

func checkTick(t *testing.T, b *Buffer, tick int64, wantOut Outcome, wantEm string, basis string) {
	t.Helper()
	out, em := b.Tick(tick)
	t.Logf("Tick(%d) -> %s %s | 依据: %s", tick, out, emitsStr(em), basis)
	if out != wantOut {
		t.Errorf("Tick(%d): outcome = %s, want %s", tick, out, wantOut)
	}
	if got := emitsStr(em); got != wantEm {
		t.Errorf("Tick(%d): emitted = %s, want %s", tick, got, wantEm)
	}
}

// checkState asserts ST, Late and the (end, key)-ordered buffer contents.
func checkState(t *testing.T, b *Buffer, wantST, wantLate int64, wantBuf string) {
	t.Helper()
	if got := b.StreamTime(); got != wantST {
		t.Errorf("StreamTime() = %d, want %d", got, wantST)
	}
	if got := b.Late(); got != wantLate {
		t.Errorf("Late() = %d, want %d", got, wantLate)
	}
	if got := bufferedStr(b); got != wantBuf {
		t.Errorf("Buffered() = %s, want %s", got, wantBuf)
	}
	t.Logf("状态: ST=%d Late=%d Buffered=%s", b.StreamTime(), b.Late(), bufferedStr(b))
}

// TestExampleEmitEarly replays the EMIT_EARLY worked example from the spec.
func TestExampleEmitEarly(t *testing.T) {
	b := mustNew(t, 10, 5, 2, EmitEarly)

	checkUpdate(t, b, "a", 3, 1, OutcomeOK, "[]", "新条目 (a,0)，缓冲 1<=2，无关闭无早发")
	checkState(t, b, 3, 0, "[(a,0,1,3)]")

	checkUpdate(t, b, "b", 12, 2, OutcomeOK, "[]", "新条目 (b,10)，缓冲 2<=2")
	checkState(t, b, 12, 0, "[(a,0,1,3) (b,10,2,12)]")

	checkUpdate(t, b, "a", 14, 3, OutcomeOK, "[(a,0,1,3,Early)]",
		"ST'=14 无关闭(15,25>14)；rest=2 needNew=1 超 E=2，早发 (end,key) 最小者 (a,0)")
	checkState(t, b, 14, 0, "[(a,10,3,14) (b,10,2,12)]")

	checkUpdate(t, b, "a", 2, 9, OutcomeOK, "[(a,10,3,14,Early)]",
		"end+G=15>ST=14 不迟到；(a,0) 已被早发故 needNew=1；既有 (a,10),(b,10) end 相同取 key 小者")
	checkState(t, b, 14, 0, "[(a,0,9,2) (b,10,2,12)]")

	checkUpdate(t, b, "c", 30, 1, OutcomeOK, "[(a,0,9,2,Final) (b,10,2,12,Final)]",
		"ST'=30，关闭时刻 15、25 均不大于 30，按 (end,key) 依次 Final")
	checkState(t, b, 30, 0, "[(c,30,1,30)]")
}

// TestExampleShutdown replays the SHUTDOWN worked example from the spec.
func TestExampleShutdown(t *testing.T) {
	b := mustNew(t, 10, 5, 1, Shutdown)

	checkUpdate(t, b, "a", 3, 1, OutcomeOK, "[]", "新条目 (a,0)，缓冲 1<=1")
	checkState(t, b, 3, 0, "[(a,0,1,3)]")

	checkUpdate(t, b, "b", 12, 2, OutcomeRejected, "[]",
		"ST'=12 无关闭；rest=1 needNew=1 > E=1，SHUTDOWN 拒绝且状态不变")
	checkState(t, b, 3, 0, "[(a,0,1,3)]")

	checkUpdate(t, b, "c", 16, 3, OutcomeOK, "[(a,0,1,3,Final)]",
		"ST'=16 使 (a,0) 关闭时刻 15<=16，先 Final 腾出名额再写入 (c,10)")
	checkState(t, b, 16, 0, "[(c,10,3,16)]")

	checkUpdate(t, b, "c", 17, 4, OutcomeOK, "[]", "覆盖 (c,10)，needNew=0 不占名额")
	checkState(t, b, 17, 0, "[(c,10,4,17)]")

	checkUpdate(t, b, "a", 25, 5, OutcomeOK, "[(c,10,4,17,Final)]",
		"ST'=25，(c,10) 关闭时刻 25<=25 恰关闭，Final 后写入 (a,20)")
	checkState(t, b, 25, 0, "[(a,20,5,25)]")
}

// TestLateBoundary: end+G == ST 判迟到；end+G == ST+1 被接受。
func TestLateBoundary(t *testing.T) {
	t.Run("equal is late", func(t *testing.T) {
		b := mustNew(t, 10, 5, 4, EmitEarly)
		checkUpdate(t, b, "a", 15, 1, OutcomeOK, "[]", "建立 ST=15")
		// (b,0) 的关闭时刻 = 0+10+5 = 15 == ST，恰为迟到。
		checkUpdate(t, b, "b", 3, 2, OutcomeLate, "[]", "end+G=15 <= ST=15，丢弃并计数")
		checkState(t, b, 15, 1, "[(a,10,1,15)]")
	})
	t.Run("one above accepted", func(t *testing.T) {
		b := mustNew(t, 10, 5, 4, EmitEarly)
		checkUpdate(t, b, "a", 14, 1, OutcomeOK, "[]", "建立 ST=14")
		// (b,0) 的关闭时刻 = 15 > ST=14，差 1 被接受。
		checkUpdate(t, b, "b", 3, 2, OutcomeOK, "[]", "end+G=15 > ST=14，不迟到")
		checkState(t, b, 14, 0, "[(b,0,2,3) (a,10,1,14)]")
	})
}

// TestOldTsAccepted: ts 小于 ST 但窗口未关闭的更新被接受且不推进 ST。
func TestOldTsAccepted(t *testing.T) {
	b := mustNew(t, 10, 5, 4, EmitEarly)
	checkUpdate(t, b, "a", 20, 1, OutcomeOK, "[]", "建立 ST=20")
	checkUpdate(t, b, "b", 13, 2, OutcomeOK, "[]",
		"ts=13 < ST=20，但 (b,10) 关闭时刻 25 > 20，接受；ST 保持 20")
	checkState(t, b, 20, 0, "[(b,10,2,13) (a,20,1,20)]")
}

// TestOverwriteNoSlot: 覆盖已有条目不占名额（SHUTDOWN 下也不被拒）。
func TestOverwriteNoSlot(t *testing.T) {
	b := mustNew(t, 10, 5, 1, Shutdown)
	checkUpdate(t, b, "a", 3, 1, OutcomeOK, "[]", "新条目 (a,0)")
	checkUpdate(t, b, "a", 7, 2, OutcomeOK, "[]", "覆盖 (a,0)，needNew=0，rest+0=1<=E")
	checkState(t, b, 7, 0, "[(a,0,2,7)]")
	checkUpdate(t, b, "b", 8, 3, OutcomeRejected, "[]", "(b,0) 是新条目，rest=1 needNew=1 > E=1，拒绝")
	checkState(t, b, 7, 0, "[(a,0,2,7)]")
}

// TestEarlyOrderAndNeverSelf: 早发取 (end,key) 最小者，且本次写入条目永不被早发。
func TestEarlyOrderAndNeverSelf(t *testing.T) {
	t.Run("smallest end then key", func(t *testing.T) {
		b := mustNew(t, 10, 0, 2, EmitEarly)
		checkUpdate(t, b, "b", 1, 1, OutcomeOK, "[]", "(b,0) end=10")
		checkUpdate(t, b, "a", 2, 2, OutcomeOK, "[]", "(a,0) end=10，缓冲满")
		checkUpdate(t, b, "c", 3, 3, OutcomeOK, "[(a,0,2,2,Early)]",
			"x=1，(a,0) 与 (b,0) end 相同，key 小者 (a,0) 被早发")
		checkState(t, b, 3, 0, "[(b,0,1,1) (c,0,3,3)]")
	})
	t.Run("never self", func(t *testing.T) {
		b := mustNew(t, 10, 0, 1, EmitEarly)
		checkUpdate(t, b, "z", 25, 1, OutcomeOK, "[]", "(z,20) end=30")
		// 新条目 (a,20) 的 (end,key)=(30,a) 小于 (z,20) 的 (30,z)，
		// 但本次写入条目永不被早发，故被早发的是 (z,20)。
		checkUpdate(t, b, "a", 28, 2, OutcomeOK, "[(z,20,1,25,Early)]",
			"x=1，早发候选不含本次条目，(z,20) 被早发")
		checkState(t, b, 28, 0, "[(a,20,2,28)]")
	})
}

// TestEarlyThenFinalAgain: 早发后同一 (key,ws) 再更新成为新条目，之后以 Final 再发一次。
func TestEarlyThenFinalAgain(t *testing.T) {
	b := mustNew(t, 10, 5, 2, EmitEarly)
	checkUpdate(t, b, "a", 3, 1, OutcomeOK, "[]", "(a,0) 入缓冲")
	checkUpdate(t, b, "b", 12, 2, OutcomeOK, "[]", "(b,10) 入缓冲，缓冲满")
	checkUpdate(t, b, "c", 14, 3, OutcomeOK, "[(a,0,1,3,Early)]",
		"rest=2 needNew=1 > E=2，(end,key) 最小者 (a,0) 被早发")
	checkState(t, b, 14, 0, "[(b,10,2,12) (c,10,3,14)]")
	// (a,0) 已不在缓冲，本次成为新条目；关闭时刻 15 > ST=12，不迟到。
	checkUpdate(t, b, "a", 2, 9, OutcomeOK, "[(b,10,2,12,Early)]",
		"(a,0) 重新作为新条目写入；rest=2 needNew=1 再触发早发，(b,10) 被早发")
	checkState(t, b, 14, 0, "[(a,0,9,2) (c,10,3,14)]")
	checkTick(t, b, 15, OutcomeOK, "[(a,0,9,2,Final)]", "关闭时刻 15<=15，同一 (a,0) 以 Final 再发一次")
	checkState(t, b, 15, 0, "[(c,10,3,14)]")
}

// TestOverwriteLastTsDecreases: 覆盖时 lastTs 取本次 ts，即使更小。
func TestOverwriteLastTsDecreases(t *testing.T) {
	b := mustNew(t, 10, 5, 2, EmitEarly)
	checkUpdate(t, b, "a", 8, 1, OutcomeOK, "[]", "(a,0) lastTs=8")
	checkUpdate(t, b, "a", 2, 9, OutcomeOK, "[]",
		"ts=2 < ST=8 但窗口未关闭，覆盖 (a,0)，lastTs 变小为 2")
	checkState(t, b, 8, 0, "[(a,0,9,2)]")
}

// TestFinalBeforeEarly: 验证容量恒等式——只要关闭集合非空就不会触发早发，
// 因此同一批次内 Final 必先于 Early（实现按此顺序拼接发出序列）。
func TestFinalBeforeEarly(t *testing.T) {
	b := mustNew(t, 10, 5, 2, EmitEarly)
	checkUpdate(t, b, "a", 3, 1, OutcomeOK, "[]", "(a,0) 关闭时刻 15")
	checkUpdate(t, b, "b", 12, 2, OutcomeOK, "[]", "(b,10) 关闭时刻 25，缓冲满")
	// ST'=16 关闭 (a,0)：rest=1 needNew=1 <= E=2，不触发早发。
	checkUpdate(t, b, "c", 16, 3, OutcomeOK, "[(a,0,1,3,Final)]",
		"关闭集合非空时 rest+needNew = E-closing+1 <= E，本批只有 Final 没有 Early")
	checkState(t, b, 16, 0, "[(b,10,2,12) (c,10,3,16)]")
}

// TestTickBoundary: Tick 恰等于关闭时刻发出，差 1 不发出。
func TestTickBoundary(t *testing.T) {
	b := mustNew(t, 10, 5, 4, EmitEarly)
	checkUpdate(t, b, "a", 3, 1, OutcomeOK, "[]", "(a,0) 关闭时刻 15")
	checkTick(t, b, 14, OutcomeOK, "[]", "14 < 15，不发出")
	checkState(t, b, 14, 0, "[(a,0,1,3)]")
	checkTick(t, b, 15, OutcomeOK, "[(a,0,1,3,Final)]", "15 <= 15，恰发出")
	checkState(t, b, 15, 0, "[]")
}

// TestTickRegressionAndNoop: Tick 回退被拒绝，等于 ST 是空操作。
func TestTickRegressionAndNoop(t *testing.T) {
	b := mustNew(t, 10, 5, 4, EmitEarly)
	checkUpdate(t, b, "a", 3, 1, OutcomeOK, "[]", "(a,0) 关闭时刻 15")
	checkTick(t, b, 10, OutcomeOK, "[]", "推进 ST 到 10")
	checkTick(t, b, 9, OutcomeRejected, "[]", "9 < ST=10，回退被拒绝，状态不变")
	checkTick(t, b, 10, OutcomeOK, "[]", "等于 ST，空操作")
	checkState(t, b, 10, 0, "[(a,0,1,3)]")
}

// TestLateDoesNotAdvanceST: 迟到更新不推进 ST、不改缓冲，只增加迟到计数。
func TestLateDoesNotAdvanceST(t *testing.T) {
	b := mustNew(t, 10, 5, 4, EmitEarly)
	checkUpdate(t, b, "a", 20, 1, OutcomeOK, "[]", "建立 ST=20")
	checkUpdate(t, b, "b", 1, 2, OutcomeLate, "[]",
		"(b,0) 关闭时刻 15 <= ST=20，迟到丢弃；ST 保持 20")
	checkState(t, b, 20, 1, "[(a,20,1,20)]")
	checkUpdate(t, b, "c", 9, 3, OutcomeLate, "[]", "(c,0) 关闭时刻 15 <= 20，再次迟到")
	checkState(t, b, 20, 2, "[(a,20,1,20)]")
}

// TestRejectedKeepsState: 各类被拒绝的操作不改变 ST、缓冲与迟到计数。
func TestRejectedKeepsState(t *testing.T) {
	b := mustNew(t, 10, 5, 1, Shutdown)
	checkUpdate(t, b, "a", 3, 1, OutcomeOK, "[]", "建立状态")
	checkUpdate(t, b, "b", 4, 2, OutcomeRejected, "[]", "SHUTDOWN 满，拒绝")
	checkUpdate(t, b, "", 4, 2, OutcomeInvalid, "[]", "空 key，参数非法")
	checkUpdate(t, b, "c", -1, 2, OutcomeInvalid, "[]", "ts 越界，参数非法")
	checkUpdate(t, b, "c", 4, 1_000_000_000_001, OutcomeInvalid, "[]", "value 越界，参数非法")
	checkTick(t, b, 2, OutcomeRejected, "[]", "Tick 回退，拒绝")
	checkTick(t, b, -1, OutcomeInvalid, "[]", "t 越界，参数非法")
	checkState(t, b, 3, 0, "[(a,0,1,3)]")
}

// TestInvalidParams: 构造参数与操作参数的边界校验。
func TestInvalidParams(t *testing.T) {
	bad := []struct {
		s, g, e int64
		p       Policy
	}{
		{0, 0, 1, EmitEarly}, {1_000_000_001, 0, 1, EmitEarly},
		{1, -1, 1, EmitEarly}, {1, 1_000_000_001, 1, EmitEarly},
		{1, 0, 0, EmitEarly}, {1, 0, 1_000_001, EmitEarly},
		{1, 0, 1, Policy(7)},
	}
	for _, c := range bad {
		if _, err := New(c.s, c.g, c.e, c.p); err == nil {
			t.Errorf("New(%d, %d, %d, %d) 应报错", c.s, c.g, c.e, int(c.p))
		}
	}
	for _, c := range [][3]int64{{1, 0, 1}, {1_000_000_000, 1_000_000_000, 1_000_000}} {
		if _, err := New(c[0], c[1], c[2], Shutdown); err != nil {
			t.Errorf("New(%d, %d, %d) 不应报错: %v", c[0], c[1], c[2], err)
		}
	}

	b := mustNew(t, 10, 5, 4, EmitEarly)
	checkUpdate(t, b, "k", 0, 0, OutcomeOK, "[]", "ts=0 合法")
	checkUpdate(t, b, "v", 1_000_000_000_000_000, -1_000_000_000_000, OutcomeOK, "[(k,0,0,0,Final)]",
		"ts=1e15、value=-1e12 边界合法")
	checkUpdate(t, b, "w", 1_000_000_000_000_001, 0, OutcomeInvalid, "[]", "ts>1e15 非法")
	checkUpdate(t, b, "w", 0, 1_000_000_000_001, OutcomeInvalid, "[]", "value>1e12 非法")
	checkTick(t, b, 1_000_000_000_000_001, OutcomeInvalid, "[]", "t>1e15 非法")
}

// TestPeeksTwoTiers: 同一个无发出的 Update，在缓冲有 10 个与 10000 个
// 均未到关闭时刻的条目时，peeks 增量必须相等，且不超过 发出数+2。
func TestPeeksTwoTiers(t *testing.T) {
	probe := func(t *testing.T, n int) int64 {
		t.Helper()
		b := mustNew(t, 1000, 1_000_000_000, 1_000_000, EmitEarly)
		for i := 0; i < n; i++ {
			key := []byte(fmt.Sprintf("k%06d", i))
			if out, em := b.Update(key, int64(i)*1000+1, 1); out != OutcomeOK || len(em) != 0 {
				t.Fatalf("setup Update #%d: out=%s em=%v", i, out, em)
			}
		}
		before := b.peeks
		out, em := b.Update([]byte("probe"), 5, 1)
		delta := b.peeks - before
		t.Logf("n=%d: Update(probe,5,1) -> %s %s, peeks 增量=%d (上限=%d)",
			n, out, emitsStr(em), delta, int64(len(em))+2)
		if out != OutcomeOK || len(em) != 0 {
			t.Fatalf("n=%d: probe Update: out=%s em=%v", n, out, em)
		}
		if delta > int64(len(em))+2 {
			t.Errorf("n=%d: peeks 增量 %d 超过 发出数+2", n, delta)
		}
		return delta
	}
	d10 := probe(t, 10)
	d10000 := probe(t, 10000)
	if d10 != d10000 {
		t.Errorf("peeks 增量与缓冲规模相关: n=10 时 %d, n=10000 时 %d", d10, d10000)
	}
}
