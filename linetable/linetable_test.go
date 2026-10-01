package linetable

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// mustAppend 断言追加成功，并打印输入与判定依据。
func mustAppend(t *testing.T, tab *LineTable, pc, line int) {
	t.Helper()
	err := tab.Append(pc, line)
	t.Logf("Append(pc=%d,line=%d) -> err=%v; N=%d, 成功(含同行合并)即推进lastPC", pc, line, err, tab.n)
	if err != nil {
		t.Fatalf("Append(%d,%d) unexpected error: %v", pc, line, err)
	}
}

// mustFail 断言追加返回指定错误，并确认被拒追加未改变编码字节。
func mustFail(t *testing.T, tab *LineTable, pc, line int, want error) {
	t.Helper()
	before := append([]byte(nil), tab.Encode()...)
	err := tab.Append(pc, line)
	t.Logf("Append(pc=%d,line=%d) -> err=%v, 期望=%v; 校验次序: 越界→行非正→不递增→首条非0", pc, line, err, want)
	if err != want {
		t.Fatalf("Append(%d,%d) err=%v want %v", pc, line, err, want)
	}
	if after := tab.Encode(); !bytes.Equal(before, after) {
		t.Fatalf("rejected Append(%d,%d) mutated table: before=%x after=%x", pc, line, before, after)
	}
}

// mustQuery 断言查询结果并打印输入输出。
func mustQuery(t *testing.T, tab *LineTable, pc, wantLine int) {
	t.Helper()
	got, err := tab.LineAt(pc)
	t.Logf("LineAt(pc=%d) -> line=%d,err=%v; 规则: 起点不大于pc的最后条目", pc, got, err)
	if err != nil || got != wantLine {
		t.Fatalf("LineAt(%d)=(%d,%v) want %d", pc, got, err, wantLine)
	}
}

// TestByteBoundaries 覆盖 pc 差 63/64、行差 +63/+64/-64/-65 的逐字节边界，
// 以及 pc 恰为 N-1（成功）与 N（越界）、同行合并推进 lastPC。
func TestByteBoundaries(t *testing.T) {
	const n = 300
	tab, err := New(n)
	if err != nil {
		t.Fatal(err)
	}

	// (0,1)(64,65)(127,1)(191,64)(255,65)(299,65-合并)
	mustAppend(t, tab, 0, 1)
	mustAppend(t, tab, 64, 65)  // pcΔ64(2字节) 行Δ+64(2字节)
	mustAppend(t, tab, 127, 1)  // pcΔ63(1字节) 行Δ-64(1字节)
	mustAppend(t, tab, 191, 64) // pcΔ64(2字节) 行Δ+63(1字节)
	mustAppend(t, tab, 255, 65) // pcΔ64(2字节) 行Δ+1
	mustAppend(t, tab, n-1, 65) // pc 恰为 N-1；同行合并，不新增条目

	t.Logf("pc=N=%d 与合并后回退/重复 pc 均必须拒绝", n)
	mustFail(t, tab, n, 65, ErrPCOutOfRange)      // pc 恰为 N
	mustFail(t, tab, n-1, 65, ErrPCNotIncreasing) // 合并推进了 lastPC
	mustFail(t, tab, 298, 65, ErrPCNotIncreasing)
	mustFail(t, tab, -1, 65, ErrPCOutOfRange)

	want := []byte{
		0x00, 0x02, // (0,1): pcΔ0; 行Δ1 -> zigzag2
		0x80, 0x01, 0x80, 0x01, // (64,65): zz64=128; zz(+64)=128
		0x7e, 0x7f, // (127,1): pcΔ63 -> zz126=0x7e; 行Δ-64 -> zz127=0x7f
		0x80, 0x01, 0x7e, // (191,64): pcΔ64; 行Δ+63 -> zz126
		0x80, 0x01, 0x02, // (255,65): pcΔ64; 行Δ+1
	}
	got := tab.Encode()
	t.Logf("Encode 输出=%x, 期望=%x", got, want)
	if !bytes.Equal(got, want) {
		t.Fatalf("encode mismatch:\n got=%x\nwant=%x", got, want)
	}

	// 行差 -65（zigzag 129 = 0x81 0x01，2 字节）单独构造：(0,100),(1,35)。
	tab2, _ := New(2)
	mustAppend(t, tab2, 0, 100)
	mustAppend(t, tab2, 1, 35)
	want2 := []byte{0x00, 0xc8, 0x01, 0x02, 0x81, 0x01}
	got2 := tab2.Encode()
	t.Logf("行Δ-65: Encode=%x 期望=%x (100->zz200=c8 01; -65->zz129=81 01)", got2, want2)
	if !bytes.Equal(got2, want2) {
		t.Fatalf("encode -65 mismatch: got=%x want=%x", got2, want2)
	}
}

// TestMergeAdvancesLastPC 同行合并不新增条目，但 lastPC 必须推进。
func TestMergeAdvancesLastPC(t *testing.T) {
	tab, _ := New(10)
	mustAppend(t, tab, 0, 1)
	mustAppend(t, tab, 2, 1) // 合并
	mustFail(t, tab, 2, 1, ErrPCNotIncreasing)
	mustFail(t, tab, 1, 5, ErrPCNotIncreasing)
	mustAppend(t, tab, 3, 2) // lastPC=2，pc=3 可追加新条目

	want := []byte{0x00, 0x02, 0x06, 0x02} // 只有 (0,1),(3,2)
	got := tab.Encode()
	t.Logf("合并后 Encode=%x 期望=%x; 查询 pc=2 命中条目(0,1)", got, want)
	if !bytes.Equal(got, want) {
		t.Fatalf("got=%x want=%x", got, want)
	}
	mustQuery(t, tab, 2, 1)
	mustQuery(t, tab, 3, 2)
}

// TestAppendValidationOrder 追加错误按次序只报第一个；被拒追加不改状态。
func TestAppendValidationOrder(t *testing.T) {
	tab, _ := New(5)
	mustFail(t, tab, 5, 0, ErrPCOutOfRange) // 越界先于行非正
	mustFail(t, tab, 3, 0, ErrLineNotPositive)

	if err := tab.Append(0, 1); err != nil {
		t.Fatalf("first append: %v", err)
	}
	mustFail(t, tab, 0, 5, ErrPCNotIncreasing)

	empty, _ := New(3)
	err := empty.Append(1, 1) // 空表首条 pc 不为 0
	t.Logf("空表 Append(pc=1) -> %v, 期望 %v", err, ErrFirstPCNotZero)
	if err != ErrFirstPCNotZero {
		t.Fatalf("got %v want %v", err, ErrFirstPCNotZero)
	}
	mustFail(t, empty, 3, 1, ErrPCOutOfRange) // 越界先于首条非0
}

// TestQueryErrors 查询：表为空先报，再报越界。
func TestQueryErrors(t *testing.T) {
	tab, _ := New(3)
	if _, err := tab.LineAt(0); err != ErrEmpty {
		t.Fatalf("empty query err=%v want %v", err, ErrEmpty)
	}
	t.Logf("空表 LineAt(0) -> %v", ErrEmpty)
	mustAppend(t, tab, 0, 7)
	if _, err := tab.LineAt(3); err != ErrPCOutOfRange {
		t.Fatalf("query N err=%v", err)
	}
	if _, err := tab.LineAt(-1); err != ErrPCOutOfRange {
		t.Fatalf("query -1 err=%v", err)
	}
	mustQuery(t, tab, 2, 7)
}

// TestNewInvalidN 构造 N<1 拒绝。
func TestNewInvalidN(t *testing.T) {
	if _, err := New(0); err != ErrInvalidN {
		t.Fatalf("New(0) err=%v", err)
	}
	if _, err := Decode(0, nil); err != ErrInvalidN {
		t.Fatalf("Decode(0) err=%v", err)
	}
	t.Logf("New(0)/Decode(0) -> %v", ErrInvalidN)
}

// TestDecodeVarintErrors 三类变长整数字节错误，按字节顺序遇到第一处即报。
func TestDecodeVarintErrors(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"超过10字节仍未结束", bytes.Repeat([]byte{0x80}, 11), ErrVarintTooLong},
		{"第10字节超uint64", append(append([]byte{}, bytes.Repeat([]byte{0x80}, 9)...), 0x82, 0x00), ErrVarintTooLong},
		{"续位后截断(单字节)", []byte{0x80}, ErrVarintTruncated},
		{"续位后截断(多字节)", []byte{0x80, 0x80}, ErrVarintTruncated},
		{"合法首值后第二值截断", []byte{0x00, 0x80}, ErrVarintTruncated},
		{"首值非最短形式(80 00)", []byte{0x80, 0x00}, ErrVarintNonCanonical},
		{"第二值非最短形式", []byte{0x00, 0x02, 0x80, 0x00}, ErrVarintNonCanonical},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Decode(1000, c.data)
			t.Logf("Decode(%x) -> err=%v, 期望=%v, 依据: %s", c.data, err, c.want, c.name)
			if err != c.want {
				t.Fatalf("got %v want %v", err, c.want)
			}
		})
	}
}

// TestDecodeEntryErrors 条目级五类错误（按规定次序）及失败不留半成品。
func TestDecodeEntryErrors(t *testing.T) {
	cases := []struct {
		name string
		n    int
		data []byte
		want error
	}{
		{"首条pc差不为0", 10, []byte{0x02, 0x02}, ErrFirstPCDeltaNotZero},
		{"非首条pc差不为正(为0)", 10, []byte{0x00, 0x02, 0x00, 0x02}, ErrPCDeltaNotPositive},
		{"非首条pc差为负", 10, []byte{0x00, 0x02, 0x01, 0x02}, ErrPCDeltaNotPositive}, // zz(-1)=1
		{"累计pc不小于N", 2, []byte{0x00, 0x02, 0x04, 0x02}, ErrPCExceedsN},          // pcΔ2, curPC=2>=N
		{"非首条行差为0", 10, []byte{0x00, 0x02, 0x02, 0x00}, ErrLineDeltaZero},
		{"累计行非正(降到0)", 10, []byte{0x00, 0x02, 0x02, 0x01}, ErrLineNotCumulativePositive}, // 行Δ-1
		{"字节残留(奇数尾)", 10, []byte{0x00}, ErrVarintTruncated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tab, err := Decode(c.n, c.data)
			t.Logf("Decode(n=%d,%x) -> tab=%v,err=%v, 期望=%v; 依据: %s", c.n, c.data, tab, err, c.want, c.name)
			if err != c.want {
				t.Fatalf("got %v want %v", err, c.want)
			}
			if tab != nil {
				t.Fatalf("failed decode must not return partial table")
			}
		})
	}

	// 空字节串合法：得到空表。
	tab, err := Decode(4, nil)
	if err != nil {
		t.Fatalf("Decode(empty): %v", err)
	}
	if _, err := tab.LineAt(0); err != ErrEmpty {
		t.Fatalf("decoded empty table query: %v", err)
	}
	if b := tab.Encode(); len(b) != 0 {
		t.Fatalf("empty table re-encode = %x", b)
	}
	t.Logf("Decode(空) 合法 -> 空表, 重编码为空字节串")
}

// TestZigzagBoundaries 直接核对 zigzag 边界映射。
func TestZigzagBoundaries(t *testing.T) {
	cases := []struct {
		in   int64
		want uint64
	}{
		{0, 0}, {1, 2}, {-1, 1}, {63, 126}, {64, 128},
		{-64, 127}, {-65, 129},
	}
	for _, c := range cases {
		got := zigzag(c.in)
		back := unzigzag(got)
		t.Logf("zigzag(%d)=%d, unzigzag=%d", c.in, got, back)
		if got != c.want || back != c.in {
			t.Fatalf("zigzag(%d)=%d want %d (back=%d)", c.in, got, c.want, back)
		}
	}
}

// naiveLineTable 是按需求规则直接实现的朴素逐 pc 对照表，作为交叉校验基准。
type naiveLineTable struct {
	n     int
	lines []int // 每个 pc 一个槽；未被覆盖的槽沿用前一行
	set   []bool
	last  int
}

func newNaive(n int) *naiveLineTable {
	return &naiveLineTable{n: n, lines: make([]int, n), set: make([]bool, n), last: -1}
}

func (m *naiveLineTable) append(pc, line int) error {
	if pc < 0 || pc >= m.n {
		return ErrPCOutOfRange
	}
	if line < 1 {
		return ErrLineNotPositive
	}
	if pc <= m.last {
		if m.last < 0 {
			return ErrFirstPCNotZero
		}
		return ErrPCNotIncreasing
	}
	m.last = pc
	// 朴素对照：不管是否同行，逐 pc 铺开。
	prev := 0
	if pc > 0 {
		for i := pc - 1; i >= 0; i-- {
			if m.set[i] {
				prev = m.lines[i]
				break
			}
		}
	}
	if pc == 0 || prev != line {
		m.lines[pc] = line
		m.set[pc] = true
	}
	return nil
}

func (m *naiveLineTable) lineAt(pc int) (int, error) {
	if !m.any() {
		return 0, ErrEmpty
	}
	if pc < 0 || pc >= m.n {
		return 0, ErrPCOutOfRange
	}
	for i := pc; i >= 0; i-- {
		if m.set[i] {
			return m.lines[i], nil
		}
	}
	return 0, ErrEmpty
}

func (m *naiveLineTable) any() bool { return m.last >= 0 }

// appendSeq 重放一个 (pc,line) 序列，忽略 nil 哨兵之外的错误。
type rec struct{ pc, line int }

// TestReplayAgainstNaive 相同追加序列重放：字节串与全部 pc 查询结果完全一致，
// 并与朴素逐 pc 对照表、解码-重编码恒等律对照。
func TestReplayAgainstNaive(t *testing.T) {
	const n = 4096
	rng := rand.New(rand.NewSource(42))
	var seq []rec

	pc := 0
	line := 1
	for k := 0; k < 60; k++ {
		if k > 0 {
			pc += 1 + rng.Intn(30)
			if pc >= n {
				pc = n - 1
			}
			line += rng.Intn(21) - 10 // 允许下降，可能短暂非正，将被拒绝
		}
		seq = append(seq, rec{pc, line})
	}

	build := func() (*LineTable, *naiveLineTable) {
		tab, _ := New(n)
		ref := newNaive(n)
		for _, r := range seq {
			err1 := tab.Append(r.pc, r.line)
			err2 := ref.append(r.pc, r.line)
			if (err1 == nil) != (err2 == nil) {
				t.Fatalf("seq %+v: impl err=%v naive err=%v", r, err1, err2)
			}
			if err1 != nil && err1 != err2 {
				t.Fatalf("seq %+v: impl err=%v naive err=%v", r, err1, err2)
			}
		}
		return tab, ref
	}

	tab1, ref := build()
	tab2, _ := build()
	enc1, enc2 := tab1.Encode(), tab2.Encode()
	t.Logf("重放两次 Encode: %x 与 %x, 长度=%d", enc1, enc2, len(enc1))
	if !bytes.Equal(enc1, enc2) {
		t.Fatalf("replay encoding differs")
	}

	// 解码后再编码必须得到原字节串。
	dec, err := Decode(n, enc1)
	if err != nil {
		t.Fatalf("decode own encoding: %v", err)
	}
	if re := dec.Encode(); !bytes.Equal(re, enc1) {
		t.Fatalf("re-encode mismatch:\n got=%x\nwant=%x", re, enc1)
	}
	t.Logf("Decode(Encode(t)) 再编码恒等，共 %d 字节", len(enc1))

	// 每个 pc 的查询结果与朴素对照表一致。
	for q := 0; q < n; q++ {
		got, err1 := tab1.LineAt(q)
		want, err2 := ref.lineAt(q)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("pc=%d impl err=%v naive err=%v", q, err1, err2)
		}
		if got != want {
			t.Fatalf("pc=%d impl line=%d naive line=%d", q, got, want)
		}
		if d, derr := dec.LineAt(q); derr != nil || d != got {
			t.Fatalf("pc=%d decoded line=%d,%v vs %d", q, d, derr, got)
		}
	}
	t.Logf("全部 %d 个 pc 与朴素逐 pc 对照表一致（输入序列=%d 条）", n, len(seq))
}

// TestConcurrent 单写多读并发：追加期间并发 Encode/LineAt/Decode 必须安全，
// 编码结果恒为某个合法串行状态。
func TestConcurrent(t *testing.T) {
	const n = 2000
	tab, _ := New(n)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for pc, line := 0, 1; pc < n; pc++ {
			if err := tab.Append(pc, line); err != nil {
				t.Errorf("append %d: %v", pc, err)
				return
			}
			if pc%7 == 0 {
				line++
			}
		}
	}()

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				b := tab.Encode()
				// 任何时刻读到的编码都必须可被 Decode 接受（等价于某个串行前缀）。
				d, err := Decode(n, b)
				if err != nil {
					t.Errorf("concurrent decode %x: %v", b, err)
					return
				}
				if !bytes.Equal(d.Encode(), b) {
					t.Errorf("concurrent re-encode mismatch")
					return
				}
				if _, err := tab.LineAt((i * 37) % n); err != nil && err != ErrEmpty {
					t.Errorf("concurrent query: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	// 最终状态确定性。
	enc := tab.Encode()
	dec, err := Decode(n, enc)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("并发结束: Encode 长度=%d, 末 pc 查询=%v", len(enc), fmt.Sprint(func() int { l, _ := tab.LineAt(n - 1); return l }()))
	if !bytes.Equal(dec.Encode(), enc) {
		t.Fatalf("final re-encode mismatch")
	}
}
