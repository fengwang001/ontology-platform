package ontology

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, maxDict, maxRows int) *Writer {
	t.Helper()
	w, err := New(maxDict, maxRows)
	if err != nil {
		t.Fatalf("New(%d, %d) 报错: %v", maxDict, maxRows, err)
	}
	return w
}

func appendAll(t *testing.T, w *Writer, vals ...uint32) {
	t.Helper()
	for _, v := range vals {
		if err := w.Append(v); err != nil {
			t.Fatalf("Append(%d) 报错: %v", v, err)
		}
	}
}

func repeat(v uint32, n int) []uint32 {
	out := make([]uint32, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func mustFlush(t *testing.T, w *Writer) Page {
	t.Helper()
	p, err := w.Flush()
	if err != nil {
		t.Fatalf("Flush 报错: %v", err)
	}
	return p
}

func mustDecode(t *testing.T, w *Writer, p Page, want []uint32) {
	t.Helper()
	got, err := w.Decode(p)
	if err != nil {
		t.Fatalf("Decode 报错: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode = %v, 期望 %v", got, want)
	}
}

func TestNewParamValidation(t *testing.T) {
	bad := [][2]int{{0, 1}, {1, 0}, {0, 0}, {65537, 1}, {1, 65537}, {-1, 5}, {5, -1}, {65537, 65537}}
	for _, c := range bad {
		if _, err := New(c[0], c[1]); err != ErrParam {
			t.Errorf("New(%d, %d) = %v, 期望 ErrParam", c[0], c[1], err)
		}
	}
	good := [][2]int{{1, 1}, {65536, 65536}, {1, 65536}, {65536, 1}}
	for _, c := range good {
		if _, err := New(c[0], c[1]); err != nil {
			t.Errorf("New(%d, %d) = %v, 期望成功", c[0], c[1], err)
		}
	}
}

func TestAppendFullAndFlushEmpty(t *testing.T) {
	w := mustNew(t, 4, 3)
	appendAll(t, w, 1, 2, 3)
	if err := w.Append(4); err != ErrFull {
		t.Fatalf("缓冲满时 Append = %v, 期望 ErrFull", err)
	}
	// 被拒绝的 Append 不改变缓冲：Flush 仍应得到 [1 2 3]。
	p := mustFlush(t, w)
	if p.Rows != 3 {
		t.Fatalf("Rows = %d, 期望 3", p.Rows)
	}
	mustDecode(t, w, p, []uint32{1, 2, 3})
	if _, err := w.Flush(); err != ErrEmpty {
		t.Fatalf("空缓冲 Flush = %v, 期望 ErrEmpty", err)
	}
	// 被拒绝的 Flush 不改变状态：再 Append 后正常出页。
	appendAll(t, w, 9)
	p = mustFlush(t, w)
	mustDecode(t, w, p, []uint32{9})
}

func TestPlainPageFormat(t *testing.T) {
	w := mustNew(t, 2, 8)
	appendAll(t, w, 0x01020304, 0xA0B0C0D0, 5)
	p := mustFlush(t, w) // D=3 > maxDict=2，降级出 Plain
	if p.Enc != Plain || p.Width != 0 || p.DictLen != 0 || p.Rows != 3 {
		t.Fatalf("Plain 页元数据错误: %+v", p)
	}
	want := []byte{0x04, 0x03, 0x02, 0x01, 0xD0, 0xC0, 0xB0, 0xA0, 0x05, 0x00, 0x00, 0x00}
	if !bytes.Equal(p.Data, want) {
		t.Fatalf("Plain Data = % X, 期望 % X", p.Data, want)
	}
	mustDecode(t, w, p, []uint32{0x01020304, 0xA0B0C0D0, 5})
}

func TestDictExactlyMaxDict(t *testing.T) {
	w := mustNew(t, 4, 32)
	var vals []uint32
	for _, v := range []uint32{10, 20, 30, 40} {
		vals = append(vals, repeat(v, 5)...)
	}
	appendAll(t, w, vals...)
	p := mustFlush(t, w) // D=4 恰等于 maxDict，允许出 Dict 页
	if p.Enc != Dict {
		t.Fatalf("D == maxDict 应允许 Dict 页, 得到 %+v", p)
	}
	if p.Width != 2 || p.DictLen != 4 {
		t.Fatalf("Width=%d DictLen=%d, 期望 2/4", p.Width, p.DictLen)
	}
	if !reflect.DeepEqual(w.dictVals, []uint32{10, 20, 30, 40}) {
		t.Fatalf("字典提交次序错误: %v", w.dictVals)
	}
	mustDecode(t, w, p, vals)
}

func TestDictExceedsMaxDictByOne(t *testing.T) {
	w := mustNew(t, 4, 32)
	var vals []uint32
	for _, v := range []uint32{10, 20, 30, 40} {
		vals = append(vals, repeat(v, 5)...)
	}
	appendAll(t, w, vals...)
	if p := mustFlush(t, w); p.Enc != Dict {
		t.Fatalf("首页应为 Dict: %+v", p)
	}
	// 本页引入 5 个新值，D=9 > maxDict=4：粘滞降级，字典不变，miss 不动。
	appendAll(t, w, 50, 60, 70, 80, 90)
	p := mustFlush(t, w)
	if p.Enc != Plain {
		t.Fatalf("D 超 maxDict 应出 Plain: %+v", p)
	}
	if !w.fallback {
		t.Fatal("D 超 maxDict 应置 fallback")
	}
	if len(w.dictVals) != 4 {
		t.Fatalf("降级后字典不应增长, len=%d", len(w.dictVals))
	}
	if w.miss != 0 {
		t.Fatalf("第二步出的 Plain 不应动 miss, miss=%d", w.miss)
	}
	mustDecode(t, w, p, []uint32{50, 60, 70, 80, 90})
}

func TestZeroWidthPage(t *testing.T) {
	w := mustNew(t, 8, 16)
	appendAll(t, w, repeat(42, 5)...)
	p := mustFlush(t, w)
	if p.Enc != Dict || p.Width != 0 || p.DictLen != 1 {
		t.Fatalf("零宽页元数据错误: %+v", p)
	}
	if !bytes.Equal(p.Data, []byte{0x00, 0x03}) {
		t.Fatalf("5 个零宽索引 Data = % X, 期望 00 03", p.Data)
	}
	mustDecode(t, w, p, repeat(42, 5))

	appendAll(t, w, repeat(42, 9)...)
	p = mustFlush(t, w)
	if !bytes.Equal(p.Data, []byte{0x00, 0x12}) {
		t.Fatalf("9 个零宽索引 Data = % X, 期望 00 12", p.Data)
	}
	mustDecode(t, w, p, repeat(42, 9))
}

func TestWidthBoundaries(t *testing.T) {
	// D=2 -> w=1
	w := mustNew(t, 300, 64)
	appendAll(t, w, append(repeat(7, 10), repeat(9, 10)...)...)
	if p := mustFlush(t, w); p.Enc != Dict || p.Width != 1 {
		t.Fatalf("D=2 应得 w=1: %+v", p)
	}
	// D=3 -> w=2
	w = mustNew(t, 300, 64)
	appendAll(t, w, append(append(repeat(5, 10), repeat(6, 10)...), repeat(7, 10)...)...)
	if p := mustFlush(t, w); p.Enc != Dict || p.Width != 2 {
		t.Fatalf("D=3 应得 w=2: %+v", p)
	}
	// D=256 -> w=8
	w = mustNew(t, 300, 1024)
	var vals []uint32
	for i := 0; i < 256; i++ {
		vals = append(vals, uint32(i), uint32(i))
	}
	appendAll(t, w, vals...)
	if p := mustFlush(t, w); p.Enc != Dict || p.Width != 8 {
		t.Fatalf("D=256 应得 w=8: %+v", p)
	}
	// D=257 -> w=9
	w = mustNew(t, 300, 1024)
	vals = nil
	for i := 0; i < 257; i++ {
		vals = append(vals, uint32(i), uint32(i))
	}
	appendAll(t, w, vals...)
	if p := mustFlush(t, w); p.Enc != Dict || p.Width != 9 {
		t.Fatalf("D=257 应得 w=9: %+v", p)
	}
}

// 例一：w=2 时索引 [1×11, 0, 2, 3] 得字节 16 01 03 38 00。
func TestHybridExample1(t *testing.T) {
	w := mustNew(t, 16, 128)
	var vals []uint32
	for _, v := range []uint32{10, 20, 30, 40} {
		vals = append(vals, repeat(v, 10)...)
	}
	appendAll(t, w, vals...)
	if p := mustFlush(t, w); p.Enc != Dict {
		t.Fatalf("首页应提交字典: %+v", p)
	}
	page2 := append(repeat(20, 11), 10, 30, 40)
	appendAll(t, w, page2...)
	p := mustFlush(t, w)
	want := []byte{0x02, 0x16, 0x01, 0x03, 0x38, 0x00}
	if !bytes.Equal(p.Data, want) {
		t.Fatalf("Data = % X, 期望 % X", p.Data, want)
	}
	mustDecode(t, w, p, page2)
}

// 例二：w=3 时 [5,6,7, 1×13] 借位 f=5、L-f=8，得 03 F5 93 24 10 01；
// [5,6,7, 1×12] 时 L-f=7 整段并入，得 05 F5 93 24 49 92 04。
func TestHybridBorrow(t *testing.T) {
	setup := func() *Writer {
		w := mustNew(t, 16, 128)
		var vals []uint32
		for i := 1; i <= 8; i++ {
			vals = append(vals, repeat(uint32(i*10), 10)...)
		}
		appendAll(t, w, vals...)
		if p := mustFlush(t, w); p.Enc != Dict || p.Width != 3 {
			t.Fatalf("首页应提交 8 项字典且 w=3: %+v", p)
		}
		return w
	}
	// 索引 [5,6,7, 1×13]
	w := setup()
	page := append([]uint32{60, 70, 80}, repeat(20, 13)...)
	appendAll(t, w, page...)
	p := mustFlush(t, w)
	want := []byte{0x03, 0x03, 0xF5, 0x93, 0x24, 0x10, 0x01}
	if !bytes.Equal(p.Data, want) {
		t.Fatalf("借位恰为 8: Data = % X, 期望 % X", p.Data, want)
	}
	mustDecode(t, w, p, page)
	// 索引 [5,6,7, 1×12]
	w = setup()
	page = append([]uint32{60, 70, 80}, repeat(20, 12)...)
	appendAll(t, w, page...)
	p = mustFlush(t, w)
	want = []byte{0x03, 0x05, 0xF5, 0x93, 0x24, 0x49, 0x92, 0x04}
	if !bytes.Equal(p.Data, want) {
		t.Fatalf("借位后为 7: Data = % X, 期望 % X", p.Data, want)
	}
	mustDecode(t, w, p, page)
}

// 游程恰为 8 走 RLE，恰为 7 进文字缓冲。
func TestRunExactly8And7(t *testing.T) {
	setup := func() *Writer {
		w := mustNew(t, 4, 32)
		appendAll(t, w, append(repeat(100, 10), repeat(200, 10)...)...)
		if p := mustFlush(t, w); p.Enc != Dict || p.Width != 1 {
			t.Fatalf("首页应提交字典且 w=1: %+v", p)
		}
		return w
	}
	w := setup()
	appendAll(t, w, repeat(200, 8)...)
	p := mustFlush(t, w)
	if !bytes.Equal(p.Data, []byte{0x01, 0x10, 0x01}) {
		t.Fatalf("L=8 应走 RLE: Data = % X, 期望 01 10 01", p.Data)
	}
	mustDecode(t, w, p, repeat(200, 8))

	w = setup()
	appendAll(t, w, repeat(200, 7)...)
	p = mustFlush(t, w)
	if !bytes.Equal(p.Data, []byte{0x01, 0x03, 0x7F}) {
		t.Fatalf("L=7 应进文字缓冲: Data = % X, 期望 01 03 7F", p.Data)
	}
	mustDecode(t, w, p, repeat(200, 7))
}

// 整页单个游程。
func TestSingleRunPage(t *testing.T) {
	w := mustNew(t, 8, 64)
	appendAll(t, w, repeat(7, 50)...)
	p := mustFlush(t, w)
	if p.Enc != Dict || p.Width != 0 {
		t.Fatalf("整页单游程应为零宽 Dict 页: %+v", p)
	}
	if !bytes.Equal(p.Data, []byte{0x00, 0x64}) {
		t.Fatalf("Data = % X, 期望 00 64", p.Data)
	}
	mustDecode(t, w, p, repeat(7, 50))
}

// 末组补零：3 个文字占 1 组，其余 5 个槽补索引 0。
func TestFinalGroupPadding(t *testing.T) {
	w := mustNew(t, 8, 32)
	appendAll(t, w, append(repeat(5, 10), repeat(6, 10)...)...)
	if p := mustFlush(t, w); p.Enc != Dict {
		t.Fatalf("首页应提交字典: %+v", p)
	}
	appendAll(t, w, 5, 6, 5)
	p := mustFlush(t, w)
	if !bytes.Equal(p.Data, []byte{0x01, 0x03, 0x02}) {
		t.Fatalf("Data = % X, 期望 01 03 02", p.Data)
	}
	mustDecode(t, w, p, []uint32{5, 6, 5})
	// 把补足位改成非零，解码必须报 ErrCorrupt。
	bad := Page{Enc: Dict, Rows: 3, Width: 1, DictLen: p.DictLen, Data: []byte{0x01, 0x03, 0x0A}}
	if _, err := w.Decode(bad); err != ErrCorrupt {
		t.Fatalf("补足位非零应报 ErrCorrupt, 得到 %v", err)
	}
}

// 新增字典项成本使 Dict 页恰等于 Plain 页时选 Plain，且字典不增长。
func TestDictSizeTiePicksPlain(t *testing.T) {
	w := mustNew(t, 16, 16)
	appendAll(t, w, 100, 200, 300, 100)
	p := mustFlush(t, w)
	// H = 03 24 00（3 字节），Dict 页大小 = 1+3+4*3 = 16 = Plain 页大小。
	if p.Enc != Plain {
		t.Fatalf("大小相等应选 Plain: %+v", p)
	}
	if len(w.dictVals) != 0 {
		t.Fatalf("落选页的新值不应进字典: %v", w.dictVals)
	}
	if w.miss != 1 || w.fallback {
		t.Fatalf("miss=%d fallback=%v, 期望 1/false", w.miss, w.fallback)
	}
	mustDecode(t, w, p, []uint32{100, 200, 300, 100})
	// 之后 100 再出现时按新值重新分配索引 0。
	appendAll(t, w, repeat(100, 10)...)
	p = mustFlush(t, w)
	if p.Enc != Dict || !reflect.DeepEqual(w.dictVals, []uint32{100}) {
		t.Fatalf("落选值再出现应重新分配索引: p=%+v dict=%v", p, w.dictVals)
	}
	if !bytes.Equal(p.Data, []byte{0x00, 0x14}) {
		t.Fatalf("Data = % X, 期望 00 14", p.Data)
	}
	if w.miss != 0 {
		t.Fatalf("出 Dict 页应清零 miss, miss=%d", w.miss)
	}
}

// 降级后以后各页恒为 Plain 且字典不再增长。
func TestFallbackSticky(t *testing.T) {
	w := mustNew(t, 2, 64)
	appendAll(t, w, 1, 2, 3) // D=3 > maxDict=2，粘滞降级
	p := mustFlush(t, w)
	if p.Enc != Plain || !w.fallback {
		t.Fatalf("D 超 maxDict 应粘滞降级出 Plain: %+v", p)
	}
	for i, vals := range [][]uint32{repeat(4, 20), {5, 6, 7, 8}, repeat(4, 9)} {
		appendAll(t, w, vals...)
		p = mustFlush(t, w)
		if p.Enc != Plain || p.Width != 0 || p.DictLen != 0 {
			t.Fatalf("降级后第 %d 页应为 Plain: %+v", i, p)
		}
		if len(w.dictVals) != 0 {
			t.Fatalf("降级后字典不应增长: %v", w.dictVals)
		}
		mustDecode(t, w, p, vals)
		_ = i
	}
}

// 连续三次落选才粘滞；其间出过 Dict 页则 miss 清零。
func TestMissResetByDictPage(t *testing.T) {
	w := mustNew(t, 16, 32)
	tie := func(a, b, c uint32) { // [a b c a]：Dict 页大小恰等于 Plain 页，落选
		appendAll(t, w, a, b, c, a)
		if p := mustFlush(t, w); p.Enc != Plain {
			t.Fatalf("落选页应为 Plain: %+v", p)
		}
	}
	dict := func() { // [1×20]：必出 Dict 页并清零 miss
		appendAll(t, w, repeat(1, 20)...)
		if p := mustFlush(t, w); p.Enc != Dict {
			t.Fatalf("应出 Dict 页: %+v", p)
		}
	}
	tie(1, 2, 3)
	tie(4, 5, 6)
	if w.miss != 2 || w.fallback {
		t.Fatalf("miss=%d fallback=%v, 期望 2/false", w.miss, w.fallback)
	}
	dict()
	if w.miss != 0 {
		t.Fatalf("Dict 页应清零 miss, miss=%d", w.miss)
	}
	tie(7, 8, 9)
	tie(10, 11, 12)
	dict()
	tie(13, 14, 15)
	tie(16, 17, 18)
	if w.fallback {
		t.Fatal("两次落选不应粘滞")
	}
	tie(19, 20, 21)
	if !w.fallback {
		t.Fatal("连续三次落选应粘滞降级")
	}
	appendAll(t, w, repeat(1, 20)...)
	if p := mustFlush(t, w); p.Enc != Plain {
		t.Fatalf("降级后恒为 Plain: %+v", p)
	}
}

// 第一、二步出的 Plain 页不动 miss。
func TestEarlyPlainDoesNotTouchMiss(t *testing.T) {
	w := mustNew(t, 4, 16)
	appendAll(t, w, 1, 2, 3, 1)
	mustFlush(t, w) // miss=1
	appendAll(t, w, 4, 5, 6, 4)
	mustFlush(t, w)                  // miss=2
	appendAll(t, w, 7, 8, 9, 10, 11) // D=5 > maxDict=4：第二步出 Plain
	p := mustFlush(t, w)
	if p.Enc != Plain || !w.fallback {
		t.Fatalf("D 超 maxDict 应降级: %+v", p)
	}
	if w.miss != 2 {
		t.Fatalf("第二步出的 Plain 不应动 miss, miss=%d", w.miss)
	}
}

func TestDecodeCorrupt(t *testing.T) {
	w := mustNew(t, 16, 64)
	appendAll(t, w, append(append(repeat(10, 10), repeat(20, 10)...), repeat(30, 10)...)...)
	if p := mustFlush(t, w); p.Enc != Dict || p.DictLen != 3 {
		t.Fatalf("首页应提交 3 项字典: %+v", p)
	}
	good := Page{Enc: Dict, Rows: 4, Width: 2, DictLen: 3, Data: []byte{0x02, 0x03, 0x24, 0x00}}
	if got, err := w.Decode(good); err != nil || !reflect.DeepEqual(got, []uint32{10, 20, 30, 10}) {
		t.Fatalf("合法 Dict 页解码失败: err=%v got=%v", err, got)
	}
	cases := map[string]Page{
		"Plain 长度不足":  {Enc: Plain, Rows: 3, Data: make([]byte, 11)},
		"Plain 长度超出":  {Enc: Plain, Rows: 3, Data: make([]byte, 13)},
		"Dict 空数据":    {Enc: Dict, Rows: 1, Width: 2, DictLen: 3},
		"宽度字节不符":      {Enc: Dict, Rows: 4, Width: 3, DictLen: 3, Data: []byte{0x02, 0x03, 0x24, 0x00}},
		"RLE 计数为零":    {Enc: Dict, Rows: 4, Width: 2, DictLen: 3, Data: []byte{0x02, 0x00}},
		"位打包组数为零":     {Enc: Dict, Rows: 4, Width: 2, DictLen: 3, Data: []byte{0x02, 0x01}},
		"RLE 值截断":     {Enc: Dict, Rows: 4, Width: 2, DictLen: 3, Data: []byte{0x02, 0x04}},
		"位打包体截断":      {Enc: Dict, Rows: 4, Width: 2, DictLen: 3, Data: []byte{0x02, 0x03, 0x24}},
		"索引越界":        {Enc: Dict, Rows: 4, Width: 2, DictLen: 3, Data: []byte{0x02, 0x03, 0x03, 0x00}},
		"补足位非零":       {Enc: Dict, Rows: 4, Width: 2, DictLen: 3, Data: []byte{0x02, 0x03, 0x24, 0x80}},
		"多余字节":        {Enc: Dict, Rows: 4, Width: 2, DictLen: 3, Data: []byte{0x02, 0x03, 0x24, 0x00, 0x04, 0x00}},
		"计数超出 Rows":   {Enc: Dict, Rows: 4, Width: 2, DictLen: 3, Data: []byte{0x02, 0x12, 0x00}},
		"个数不足 Rows":   {Enc: Dict, Rows: 4, Width: 2, DictLen: 3, Data: []byte{0x02, 0x04, 0x01}},
		"varint 截断":   {Enc: Dict, Rows: 4, Width: 2, DictLen: 3, Data: []byte{0x02, 0x80}},
		"末组整组补足":      {Enc: Dict, Rows: 4, Width: 2, DictLen: 3, Data: []byte{0x02, 0x05, 0x24, 0x00, 0x00, 0x00}},
		"非法编码类型":      {Enc: Enc(9), Rows: 1},
		"DictLen 超字典": {Enc: Dict, Rows: 4, Width: 2, DictLen: 100, Data: []byte{0x02, 0x03, 0xE4, 0x03}},
	}
	for name, p := range cases {
		if _, err := w.Decode(p); err != ErrCorrupt {
			t.Errorf("%s: Decode = %v, 期望 ErrCorrupt", name, err)
		}
	}
	// 合法 Plain 页对照。
	got, err := w.Decode(Page{Enc: Plain, Rows: 2, Data: []byte{0x01, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00}})
	if err != nil || !reflect.DeepEqual(got, []uint32{1, 2}) {
		t.Fatalf("合法 Plain 页解码失败: err=%v got=%v", err, got)
	}
}

// 并发调用等价于某个串行顺序：总行数守恒、每页可解码。
func TestConcurrentAccess(t *testing.T) {
	w := mustNew(t, 64, 16)
	const workers = 8
	const perWorker = 200
	var wg sync.WaitGroup
	pages := make(chan Page, workers*perWorker)
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				v := uint32(g*1000 + i%37)
				for {
					if err := w.Append(v); err == nil {
						break
					} else if err != ErrFull {
						t.Errorf("Append = %v", err)
						return
					}
					if p, err := w.Flush(); err == nil {
						pages <- p
					} else if err != ErrEmpty {
						t.Errorf("Flush = %v", err)
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
	if p, err := w.Flush(); err == nil {
		pages <- p
	} else if err != ErrEmpty {
		t.Fatalf("最终 Flush = %v", err)
	}
	close(pages)
	total := 0
	for p := range pages {
		vals, err := w.Decode(p)
		if err != nil {
			t.Fatalf("Decode = %v", err)
		}
		if len(vals) != p.Rows {
			t.Fatalf("行数不符: %d != %d", len(vals), p.Rows)
		}
		for _, v := range vals {
			if v/1000 >= workers || v%1000 >= 37 {
				t.Fatalf("解码值越界: %d", v)
			}
		}
		total += p.Rows
	}
	if total != workers*perWorker {
		t.Fatalf("解码总行数 = %d, 期望 %d", total, workers*perWorker)
	}
}

// touches 记录编码时读取的索引个数，不超过 2×Rows。
func TestTouchesBound(t *testing.T) {
	w := mustNew(t, 16, 64)
	appendAll(t, w, append(repeat(5, 12), repeat(6, 3)...)...)
	p := mustFlush(t, w)
	if p.Enc != Dict {
		t.Fatalf("应为 Dict 页: %+v", p)
	}
	if w.touches <= 0 || w.touches > 2*p.Rows {
		t.Fatalf("touches=%d 应满足 0 < touches <= %d", w.touches, 2*p.Rows)
	}
	// 第一步（已降级）直出 Plain 不读索引；第二步试算映射会读 Rows 次。
	w2 := mustNew(t, 1, 8)
	appendAll(t, w2, 1, 2)
	if _, err := w2.Flush(); err != nil {
		t.Fatal(err)
	}
	if w2.touches != 2 {
		t.Fatalf("第二步试算映射应读 %d 次索引, touches=%d", 2, w2.touches)
	}
	appendAll(t, w2, 3)
	if _, err := w2.Flush(); err != nil {
		t.Fatal(err)
	}
	if w2.touches != 0 {
		t.Fatalf("降级直出 Plain 不应读索引, touches=%d", w2.touches)
	}
}

// 已提交的 Dict 页在字典继续增长后仍可解码（索引稳定）。
func TestCommittedPageDecodesAfterDictGrowth(t *testing.T) {
	w := mustNew(t, 16, 64)
	appendAll(t, w, append(repeat(10, 10), repeat(20, 10)...)...)
	p1 := mustFlush(t, w)
	if p1.Enc != Dict {
		t.Fatalf("首页应为 Dict: %+v", p1)
	}
	appendAll(t, w, append(repeat(30, 10), repeat(40, 10)...)...)
	p2 := mustFlush(t, w)
	if p2.Enc != Dict || p2.DictLen != 4 {
		t.Fatalf("第二页应为 Dict 且 DictLen=4: %+v", p2)
	}
	mustDecode(t, w, p1, append(repeat(10, 10), repeat(20, 10)...))
	mustDecode(t, w, p2, append(repeat(30, 10), repeat(40, 10)...))
}

// Plain 页字节布局：各值 4 字节小端。
func TestPlainPageLittleEndian(t *testing.T) {
	w := mustNew(t, 2, 8)
	appendAll(t, w, 0x01020304, 0xA0B0C0D0, 5)
	p := mustFlush(t, w)
	if p.Enc != Plain {
		t.Fatalf("应出 Plain 页: %+v", p)
	}
	var want []byte
	for _, v := range []uint32{0x01020304, 0xA0B0C0D0, 5} {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], v)
		want = append(want, b[:]...)
	}
	if !bytes.Equal(p.Data, want) {
		t.Fatalf("Plain Data = % X, 期望 % X", p.Data, want)
	}
}
