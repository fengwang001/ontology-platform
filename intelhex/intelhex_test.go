package intelhex

import (
	"bytes"
	"errors"
	"sort"
	"sync"
	"testing"
)

// encLine 按 Intel HEX 规则编码一行：LL AAAA TT data... CC。
func encLine(rtype byte, offset uint16, data []byte) string {
	raw := []byte{byte(len(data)), byte(offset >> 8), byte(offset), rtype}
	raw = append(raw, data...)
	var sum byte
	for _, b := range raw {
		sum += b
	}
	raw = append(raw, ^sum+1) // 二的补码，使总和模 256 恰为 0
	const hex = "0123456789ABCDEF"
	out := make([]byte, 1+len(raw)*2)
	out[0] = ':'
	for i, b := range raw {
		out[1+i*2] = hex[b>>4]
		out[1+i*2+1] = hex[b&0xF]
	}
	return string(out)
}

var eol = encLine(0x01, 0x0000, nil) // 类型 01 文件结束行

func flatten(segs []Segment) map[uint32]byte {
	m := map[uint32]byte{}
	for _, s := range segs {
		for i, b := range s.Data {
			m[s.Start+uint32(i)] = b
		}
	}
	return m
}

func wantErr(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

func wantOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestTableCases 逐行打印输入、输出与判定依据（含各类非法行）。
func TestTableCases(t *testing.T) {
	type tc struct {
		name string
		line string
		want error // nil 表示应接受
	}
	cases := []tc{
		{"missing colon", "00000001FF", ErrSyntax},
		{"empty line", "", ErrSyntax},
		{"odd hex digits", ":000", ErrSyntax},
		{"too short", ":00", ErrSyntax},
		{"non hex char", ":00000G01FF", ErrSyntax},
		{"declared length mismatch", ":0200000011EC", ErrSyntax},
		{"checksum mismatch", ":10010000214601360121470136007EFE09D21901FF", ErrChecksum},
		{"unknown type", ":00000006FA", ErrUnknownType},
		{"eof with data", encLine(0x01, 0x0000, []byte{0x00}), ErrInvalidRecord},
		{"eof nonzero offset", encLine(0x01, 0x0001, nil), ErrInvalidRecord},
		{"seg addr bad length", encLine(0x02, 0x0000, []byte{0x00}), ErrInvalidRecord},
		{"seg addr nonzero offset", encLine(0x02, 0x0001, []byte{0x10, 0x00}), ErrInvalidRecord},
		{"lin addr bad length", encLine(0x04, 0x0000, []byte{0x00}), ErrInvalidRecord},
		{"start3 bad length", encLine(0x03, 0x0000, []byte{0x00, 0x00, 0x00}), ErrInvalidRecord},
		{"start5 nonzero offset", encLine(0x05, 0x0001, []byte{0, 0, 0, 0}), ErrInvalidRecord},
		{"cross 64k by one", encLine(0x00, 0xFFFF, []byte{0xAA, 0xBB}), ErrOutOfRange},
		{"exact 64k boundary", encLine(0x00, 0xFFFF, []byte{0xCC}), nil},
		{"zero length at ffff", encLine(0x00, 0xFFFF, nil), nil},
	}

	l := New()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := l.AddLine(c.line)
			if c.want == nil {
				if err != nil {
					t.Fatalf("输入=%s 输出=%v 依据: 应接受但被拒绝", c.line, err)
				}
				t.Logf("输入=%s 输出=接受 依据: 语法/校验和/类型合规，边界 AAAA+LL<=0x10000", c.line)
			} else {
				wantErr(t, err, c.want)
				var le *LineError
				errors.As(err, &le)
				t.Logf("输入=%s 输出=%v 行号=%d 依据: 命中 %v", c.line, err, le.Line, c.want)
			}
		})
	}
}

// TestChecksumZeroAndWrap 覆盖校验和字段恰为 0（字节和本身为 0）
// 与进位回绕（字节和为 0x200，模 256 为 0）。
func TestChecksumZeroAndWrap(t *testing.T) {
	l := New()

	exact := ":0000000000" // LL..CC = 00 00 00 00，和恰为 0
	wantOK(t, l.AddLine(exact))
	t.Logf("输入=%s 输出=接受 依据: 字节和=0x00，校验和字段恰为 0", exact)

	wrap := ":02FFFD00010100" // 和 = 0x02+0xFF+0xFD+0x01+0x01+0x00 = 0x200
	wantOK(t, l.AddLine(wrap))
	t.Logf("输入=%s 输出=接受 依据: 字节和=0x200，进位回绕后模 256 为 0", wrap)

	bad := ":02FFFD00010101"
	wantErr(t, l.AddLine(bad), ErrChecksum)
	t.Logf("输入=%s 输出=%v 依据: 字节和模 256 非 0", bad, ErrChecksum)
}

// TestBoundary64K 覆盖 AAAA+LL 恰为 0x10000 与 0x10001。
func TestBoundary64K(t *testing.T) {
	l := New()

	atEnd := encLine(0x00, 0xFFFF, []byte{0x77}) // [0xFFFF,0x10000)
	wantOK(t, l.AddLine(atEnd))
	t.Logf("输入=%s 输出=接受 依据: 0xFFFF+1=0x10000，恰好等于边界允许", atEnd)

	over := encLine(0x00, 0xFFFF, []byte{0x77, 0x88})
	wantErr(t, l.AddLine(over), ErrOutOfRange)
	t.Logf("输入=%s 输出=%v 依据: 0xFFFF+2=0x10001，越过 64KiB 边界", over, ErrOutOfRange)
}

// TestBasesOverride 验证 02 与 04 互相覆盖后的绝对地址，且段基址不回绕。
func TestBasesOverride(t *testing.T) {
	l := New()

	lin := encLine(0x04, 0x0000, []byte{0x00, 0x01}) // 0x0001<<16 = 0x10000
	wantOK(t, l.AddLine(lin))
	d1 := encLine(0x00, 0x0010, []byte{0xA0})
	wantOK(t, l.AddLine(d1))
	t.Logf("输入=%s,%s 输出=0x10010 依据: 线性基址 0x10000+0x0010", lin, d1)

	seg := encLine(0x02, 0x0000, []byte{0x10, 0x00}) // 0x1000<<4 = 0x100000，覆盖线性基址
	wantOK(t, l.AddLine(seg))
	d2 := encLine(0x00, 0x0020, []byte{0xB0})
	wantOK(t, l.AddLine(d2))
	t.Logf("输入=%s,%s 输出=0x10020 依据: 段基址覆盖线性基址，0x1000<<4=0x10000，+0x0020 不回绕", seg, d2)

	lin2 := encLine(0x04, 0x0000, []byte{0x00, 0x02}) // 0x20000 再次覆盖段基址
	wantOK(t, l.AddLine(lin2))
	d3 := encLine(0x00, 0x0030, []byte{0xC0})
	wantOK(t, l.AddLine(d3))
	t.Logf("输入=%s,%s 输出=0x20030 依据: 线性基址再次覆盖段基址，0x20000+0x0030", lin2, d3)

	m := flatten(l.Segments())
	if m[0x10010] != 0xA0 || m[0x10020] != 0xB0 || m[0x20030] != 0xC0 {
		t.Fatalf("absolute addresses wrong: %#v", m)
	}

	// 被拒绝的 02 行（校验坏）不得改变基址。
	segBad := encLine(0x02, 0x0000, []byte{0xFF, 0xFF})
	alt := "00"
	if segBad[len(segBad)-2:] == "00" {
		alt = "FF"
	}
	segBad = segBad[:len(segBad)-2] + alt
	wantErr(t, l.AddLine(segBad), ErrChecksum)
	d4 := encLine(0x00, 0x0040, []byte{0xD0})
	wantOK(t, l.AddLine(d4))
	if flatten(l.Segments())[0x20040] != 0xD0 {
		t.Fatal("rejected base line must not change base; want byte at 0x20040")
	}
	t.Logf("输入=%s 输出=%v 依据: 拒绝行不改基址，下一条仍落在 0x20040", segBad, ErrChecksum)
}

// TestAdjacentMerge 验证首尾相接段（含乱序插入）合并为一段。
func TestAdjacentMerge(t *testing.T) {
	l := New()
	writes := []struct {
		off uint16
		b   []byte
	}{
		{0x0010, []byte{0x10, 0x11}}, // [0x10,0x12)
		{0x0012, []byte{0x12, 0x13}}, // 相接
		{0x000E, []byte{0x0E, 0x0F}}, // 前接，把两段连成 [0x0E,0x14)
		{0x0100, []byte{0x40}},       // 不相接
	}
	for _, w := range writes {
		wantOK(t, l.AddLine(encLine(0x00, w.off, w.b)))
	}
	segs := l.Segments()
	t.Logf("输出=%v 依据: [0x0E,0x14) 三段首尾相接合并，0x100 独立", segs)
	if len(segs) != 2 {
		t.Fatalf("want 2 segments, got %d: %v", len(segs), segs)
	}
	if segs[0].Start != 0x0E || !bytes.Equal(segs[0].Data, []byte{0x0E, 0x0F, 0x10, 0x11, 0x12, 0x13}) {
		t.Fatalf("merged segment wrong: %+v", segs[0])
	}
	if segs[1].Start != 0x100 || !bytes.Equal(segs[1].Data, []byte{0x40}) {
		t.Fatalf("second segment wrong: %+v", segs[1])
	}
}

// TestOverlapSameValue 相同值的第二次写入仍算重叠。
func TestOverlapSameValue(t *testing.T) {
	l := New()
	first := encLine(0x00, 0x0100, []byte{0x42, 0x43})
	wantOK(t, l.AddLine(first))
	second := encLine(0x00, 0x0101, []byte{0x43}) // 同值重叠
	wantErr(t, l.AddLine(second), ErrOverlap)
	t.Logf("输入=%s 输出=%v 依据: 0x0101 已被写入，即使字节值相同也算重叠", second, ErrOverlap)

	// 零长度数据不占字节、不做重叠判定。
	z := encLine(0x00, 0x0100, nil)
	wantOK(t, l.AddLine(z))
	t.Logf("输入=%s 输出=接受 依据: LL=0 不占字节不做重叠判定（边界检查照常，0x0100<=0x10000）", z)

	if len(flatten(l.Segments())) != 2 {
		t.Fatal("rejected overlap must not change image")
	}
}

// TestStartAddresses 03/05 只记录不进映像，后一条覆盖前一条。
func TestStartAddresses(t *testing.T) {
	l := New()
	s3 := encLine(0x03, 0x0000, []byte{0x00, 0x00, 0x12, 0x34})
	wantOK(t, l.AddLine(s3))
	addr, typ, ok := l.StartAddress()
	if !ok || typ != 0x03 || addr != 0x1234 {
		t.Fatalf("start3 wrong: addr=%#x type=%#x ok=%v", addr, typ, ok)
	}
	if len(l.Segments()) != 0 {
		t.Fatal("start address record must not enter image")
	}
	s5 := encLine(0x05, 0x0000, []byte{0xDE, 0xAD, 0xBE, 0xEF})
	wantOK(t, l.AddLine(s5))
	addr, typ, ok = l.StartAddress()
	if !ok || typ != 0x05 || addr != 0xDEADBEEF {
		t.Fatalf("start5 override wrong: addr=%#x type=%#x", addr, typ)
	}
	t.Logf("输入=%s,%s 输出=起始地址 %#X (类型 05) 依据: 后一条覆盖前一条，均不进映像", s3, s5, addr)
}

// TestEOFAndFinish 覆盖 EOF 之后拒绝、行序号与 Finish 要求。
func TestEOFAndFinish(t *testing.T) {
	l := New()
	if err := l.Finish(); !errors.Is(err, ErrNotFinished) {
		t.Fatalf("Finish before EOF: want %v got %v", ErrNotFinished, err)
	}

	// 让下一条数据与已有映像重叠：重叠判定优先于“EOF 之后”。
	d := encLine(0x00, 0x0000, []byte{0x01})
	wantOK(t, l.AddLine(d))
	wantOK(t, l.AddLine(eol))

	dup := encLine(0x00, 0x0000, []byte{0x01})
	wantErr(t, l.AddLine(dup), ErrOverlap)
	t.Logf("输入=%s 输出=%v 依据: 重叠判定优先于 EOF 之后", dup, ErrOverlap)

	wantErr(t, l.AddLine(encLine(0x00, 0x0010, []byte{0x02})), ErrAfterEOF)
	wantErr(t, l.AddLine(encLine(0x04, 0x0000, []byte{0, 0})), ErrAfterEOF)

	// 语法坏行在 EOF 之后仍按优先级报语法，且同样占用行序号。
	err := l.AddLine("garbage")
	wantErr(t, err, ErrSyntax)
	le := err.(*LineError)
	if le.Line != 6 {
		t.Fatalf("line numbering wrong: want 6 got %d (rejected lines count)", le.Line)
	}
	t.Logf("输入=garbage 输出=%v 依据: 语法优先；拒绝行也占序号，序号=%d", err, le.Line)

	if err := l.Finish(); err != nil {
		t.Fatalf("Finish after EOF: %v", err)
	}
}

// lcg 是确定性伪随机源，保证相同序列可重放。
type lcg struct{ s uint32 }

func (g *lcg) next() uint32 {
	g.s = g.s*1103515245 + 12345
	return g.s
}

// refModel 是按字节 map 的朴素参考实现。
type refModel struct {
	base     uint64
	mem      map[uint64]byte
	errCount int
}

// TestNaiveCrossCheck 与逐字节数组（map）朴素映像对照。
func TestNaiveCrossCheck(t *testing.T) {
	l := New()
	ref := &refModel{mem: map[uint64]byte{}}
	g := &lcg{s: 20261001}
	var replay []string

	const ops = 600
	for i := 0; i < ops; i++ {
		var line string
		var want error
		r := g.next() % 100
		switch {
		case r < 55: // 数据记录
			off := uint16(g.next() % 0x10012) // 偶尔越过边界
			n := int(g.next() % 4)
			data := []byte{byte(g.next()), byte(g.next()), byte(g.next())}[:n]
			line = encLine(0x00, off, data)
			start := ref.base + uint64(off)
			switch {
			case int(off)+n > 0x10000:
				want = ErrOutOfRange
			case n > 0 && ref.overlaps(start, start+uint64(n)):
				want = ErrOverlap
			default:
				for j := 0; j < n; j++ {
					ref.mem[start+uint64(j)] = data[j]
				}
			}
		case r < 65: // 02 段地址
			v := uint16(g.next())
			line = encLine(0x02, 0x0000, []byte{byte(v >> 8), byte(v)})
			ref.base = uint64(v) << 4
		case r < 75: // 04 线性地址（互相覆盖）
			v := uint16(g.next() % 0x100) // 限制规模便于观察
			line = encLine(0x04, 0x0000, []byte{byte(v >> 8), byte(v)})
			ref.base = uint64(v) << 16
		case r < 82: // 03/05 起始地址
			rt := []byte{0x03, 0x05}[g.next()%2]
			d := []byte{byte(g.next()), byte(g.next()), byte(g.next()), byte(g.next())}
			line = encLine(rt, 0x0000, d)
		case r < 90: // 校验和损坏
			good := encLine(0x00, uint16(g.next()%0x10), []byte{byte(g.next())})
			// 保持偶数个字符（替换而非插入），并保证与原校验字节不同。
			alt := "00"
			if good[len(good)-2:] == "00" {
				alt = "FF"
			}
			line = good[:len(good)-2] + alt
			want = ErrChecksum
		case r < 95: // 奇数个十六进制字符
			line = ":010203"
			want = ErrSyntax
		default: // 未知类型 06
			line = ":00000006FA"
			want = ErrUnknownType
		}

		replay = append(replay, line)
		err := l.AddLine(line)
		if want != nil {
			wantErr(t, err, want)
			ref.errCount++
		} else if err != nil {
			t.Fatalf("op %d line %s unexpectedly rejected: %v", i, line, err)
		}
		if i < 12 {
			t.Logf("op=%d 输入=%s 输出=%v 依据: 与朴素 map 同规则判定", i, line, err)
		}
	}

	wantOK(t, l.AddLine(eol))
	replay = append(replay, eol)
	wantOK(t, l.Finish())

	got := flatten(l.Segments())
	if len(got) != len(ref.mem) {
		t.Fatalf("image size mismatch: loader=%d naive=%d", len(got), len(ref.mem))
	}
	for a, b := range ref.mem {
		if got[uint32(a)] != b {
			t.Fatalf("byte at %#x: loader=%#x naive=%#x", a, got[uint32(a)], b)
		}
	}

	// 段连续性不变量：除段边界外相邻地址必相接。
	segs := l.Segments()
	for i := 1; i < len(segs); i++ {
		prevEnd := segs[i-1].Start + uint32(len(segs[i-1].Data))
		if prevEnd >= segs[i].Start {
			t.Fatalf("segments not disjoint/ordered: %+v %+v", segs[i-1], segs[i])
		}
	}
	t.Logf("对照通过: 操作=%d 拒绝=%d 映像字节=%d 段数=%d 依据: 与逐字节 map 完全一致且段升序",
		ops, ref.errCount, len(ref.mem), len(segs))

	// 确定性重放：相同行序列得到完全相同的映像与错误序列。
	l2 := New()
	var errs1, errs2 []string
	for _, line := range replay {
		e1 := l.AddLine(line) // l 已 Finish，全部应为 ErrAfterEOF（语法坏行仍为语法）
		e2 := l2.AddLine(line)
		if e1 != nil {
			errs1 = append(errs1, e1.Error())
		}
		if e2 != nil {
			errs2 = append(errs2, e2.Error())
		}
	}
	if len(errs2) != ref.errCount {
		t.Fatalf("replay rejection count = %d, want %d", len(errs2), ref.errCount)
	}
	got2 := flatten(l2.Segments())
	if len(got2) != len(got) {
		t.Fatal("replayed image differs in size")
	}
	for a, b := range got {
		if got2[a] != b {
			t.Fatalf("replay differs at %#x", a)
		}
	}
	if len(errs1) != len(replay) {
		t.Fatalf("post-EOF replay: all lines must error, got %d/%d", len(errs1), len(replay))
	}
	t.Logf("重放一致: 相同序列产生相同映像（%d 字节）与错误序列（%d 条）", len(got2), len(errs2))
}

func (m *refModel) overlaps(start, end uint64) bool {
	for a := start; a < end; a++ {
		if _, ok := m.mem[a]; ok {
			return true
		}
	}
	return false
}

// TestConcurrentQueries 并发 AddLine/查询，等价于某个串行顺序（-race 检测）。
func TestConcurrentQueries(t *testing.T) {
	l := New()
	const goroutines = 8
	var readers, writers sync.WaitGroup

	stop := make(chan struct{})
	readers.Add(1)
	go func() { // 查询者
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = l.Segments()
				_, _, _ = l.StartAddress()
				_ = l.Finish()
			}
		}
	}()

	// 每个写者使用互不相交的偏移带，任何串行交错都不会重叠。
	for g := 0; g < goroutines; g++ {
		writers.Add(1)
		go func(id int) {
			defer writers.Done()
			baseOff := uint16(id * 0x40) // 每条 2 字节，步长 0x40 保证带内带间均不相交
			for k := 0; k < 20; k++ {
				off := baseOff + uint16(k*2)
				if err := l.AddLine(encLine(0x00, off, []byte{byte(id), byte(k)})); err != nil {
					t.Errorf("writer %d off %#x: %v", id, off, err)
					return
				}
			}
		}(g)
	}

	// 等全部写者结束再发 EOF（EOF 与写入并发会合法地产生 ErrAfterEOF）。
	writers.Wait()
	if err := l.AddLine(eol); err != nil {
		t.Fatalf("EOF: %v", err)
	}
	close(stop)
	readers.Wait()

	m := flatten(l.Segments())
	if len(m) != goroutines*20*2 {
		t.Fatalf("concurrent image bytes = %d", len(m))
	}
	t.Logf("并发通过: %d 个写者不相交偏移带，最终 %d 字节，结果等价某串行顺序",
		goroutines, len(m))
}

// TestSegmentsAreCopies 保证查询返回副本，调用方修改不影响内部映像。
func TestSegmentsAreCopies(t *testing.T) {
	l := New()
	wantOK(t, l.AddLine(encLine(0x00, 0x0000, []byte{0x11, 0x22})))
	s := l.Segments()
	s[0].Data[0] = 0xFF
	if flatten(l.Segments())[0] != 0x11 {
		t.Fatal("Segments must return defensive copies")
	}
	// 排序稳定性抽查
	addrs := make([]uint32, 0)
	for _, seg := range l.Segments() {
		addrs = append(addrs, seg.Start)
	}
	if !sort.SliceIsSorted(addrs, func(i, j int) bool { return addrs[i] < addrs[j] }) {
		t.Fatal("segments not sorted")
	}
}
