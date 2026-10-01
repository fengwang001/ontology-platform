package zlibstore

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// encodeNaive 是规格的朴素整体实现：头 + 每 65535 字节一块 + 尾部，
// 作为流式编码器逐字节输出的对照基准。
func encodeNaive(data []byte) []byte {
	var out bytes.Buffer
	out.Write(zlibHeader[:])
	full := len(data) / maxStoredLen
	for i := 0; i < full; i++ {
		chunk := data[i*maxStoredLen : (i+1)*maxStoredLen]
		final := byte(0)
		out.WriteByte(final)
		out.WriteByte(byte(len(chunk)))
		out.WriteByte(byte(len(chunk) >> 8))
		nlen := ^uint16(len(chunk))
		out.WriteByte(byte(nlen))
		out.WriteByte(byte(nlen >> 8))
		out.Write(chunk)
	}
	chunk := data[full*maxStoredLen:] // Close 阶段的终块，允许 0 字节
	out.WriteByte(1)
	out.WriteByte(byte(len(chunk)))
	out.WriteByte(byte(len(chunk) >> 8))
	nlen := ^uint16(len(chunk))
	out.WriteByte(byte(nlen))
	out.WriteByte(byte(nlen >> 8))
	out.Write(chunk)
	sum := adler32(data)
	out.WriteByte(byte(sum >> 24))
	out.WriteByte(byte(sum >> 16))
	out.WriteByte(byte(sum >> 8))
	out.WriteByte(byte(sum))
	return out.Bytes()
}

// encodeStream 用给定写切分驱动流式编码器。
func encodeStream(t *testing.T, data []byte, split []int) []byte {
	t.Helper()
	var out bytes.Buffer
	enc := NewEncoder(&out)
	pos := 0
	for _, size := range split {
		if pos >= len(data) {
			break
		}
		if size > len(data)-pos {
			size = len(data) - pos
		}
		n, err := enc.Write(data[pos : pos+size])
		if err != nil || n != size {
			t.Fatalf("Write(%d) = %d, %v", size, n, err)
		}
		pos += size
	}
	if pos != len(data) {
		t.Fatalf("split plan consumed %d of %d bytes", pos, len(data))
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return out.Bytes()
}

func repeatOne(n int) []int {
	plan := make([]int, n)
	for i := range plan {
		plan[i] = 1
	}
	return plan
}

func repeatSize(n, size int) []int {
	plan := make([]int, 0, n/size+1)
	for remaining := n; remaining > 0; {
		s := size
		if s > remaining {
			s = remaining
		}
		plan = append(plan, s)
		remaining -= s
	}
	return plan
}

// allSplits 为长度 n 的输入生成切分方案：整块、逐字节，
// 以及所有“在某个偏移处一刀两段”的切分点。
func allSplits(n int) [][]int {
	splits := [][]int{{n}}
	if n > 1 {
		splits = append(splits, repeatOne(n))
		for cut := 1; cut < n; cut++ {
			splits = append(splits, []int{cut, n - cut})
		}
	}
	return splits
}

// manySplits 为大输入生成确定性的多种切分方案，
// 覆盖 1、2、3、7、64、4096、65535、65536 等关键块大小与随机方案。
func manySplits(n int, rng *rand.Rand) [][]int {
	base := []int{1, 2, 3, 7, 64, 4096, 65535, 65536}
	splits := [][]int{{n}, repeatOne(n)}
	for _, size := range base {
		plan := make([]int, 0, n/size+1)
		for remaining := n; remaining > 0; {
			s := size
			if s > remaining {
				s = remaining
			}
			plan = append(plan, s)
			remaining -= s
		}
		splits = append(splits, plan)
	}
	for i := 0; i < 24; i++ {
		plan := make([]int, 0, n/64+1)
		for remaining := n; remaining > 0; {
			size := 1 + rng.Intn(70000)
			if size > remaining {
				size = remaining
			}
			plan = append(plan, size)
			remaining -= size
		}
		splits = append(splits, plan)
	}
	return splits
}

// feedResult 汇总一次完整喂入的结果指纹。
type feedResult struct {
	data      []byte
	delivered int64
	err       error // 第一个错误（Write 或 Close）
}

// decodeWithSplit 按 split 方案把 stream 喂给新解码器并在最后 Close。
func decodeWithSplit(t *testing.T, stream []byte, split []int) feedResult {
	t.Helper()
	dec := NewDecoder()
	var got bytes.Buffer
	var first error
	pos := 0
	plan := split
	if plan == nil {
		plan = []int{len(stream)}
	}
	for _, size := range plan {
		if pos >= len(stream) {
			break
		}
		if size > len(stream)-pos {
			size = len(stream) - pos
		}
		out, err := dec.Write(stream[pos : pos+size])
		got.Write(out)
		if err != nil && first == nil {
			first = err
		}
		pos += size
	}
	if pos != len(stream) {
		t.Fatalf("split plan consumed %d of %d stream bytes", pos, len(stream))
	}
	closeErr := dec.Close()
	if first == nil {
		first = closeErr
	}
	return feedResult{data: got.Bytes(), delivered: dec.Delivered(), err: first}
}

// assertSplitsEquivalent 遍历所有切分点，要求交付数据、首错误、
// Delivered 计数在每种切分（含逐字节）下完全一致。
func assertSplitsEquivalent(t *testing.T, stream []byte, want feedResult) {
	t.Helper()
	for _, split := range allSplits(len(stream)) {
		got := decodeWithSplit(t, stream, split)
		assertResult(t, split, got, want)
	}
}

func assertResult(t *testing.T, split []int, got, want feedResult) {
	t.Helper()
	if !bytes.Equal(got.data, want.data) {
		t.Fatalf("split %v: delivered data %x != %x", split, got.data, want.data)
	}
	if got.delivered != want.delivered {
		t.Fatalf("split %v: Delivered %d != %d", split, got.delivered, want.delivered)
	}
	if !sameError(got.err, want.err) {
		t.Fatalf("split %v: err %v != %v", split, got.err, want.err)
	}
}

// cutCuts 为长流生成有代表性的“一刀两段”位置：
// 流首与每个已知结构偏移附近 ±8 字节（块边界、LEN/NLEN、尾部最易出问题），
// 其余位置按 5000 步长均匀采样；上限 ~256 点，保证 O(n) 测试成本。
func cutCuts(length int, offsets []int) []int {
	seen := map[int]bool{}
	var cuts []int
	add := func(c int) {
		if c >= 1 && c < length && !seen[c] {
			seen[c] = true
			cuts = append(cuts, c)
		}
	}
	for _, base := range offsets {
		for delta := -8; delta <= 8; delta++ {
			add(base + delta)
		}
	}
	for c := 1; c < length; c += 5000 {
		add(c)
	}
	add(length - 1)
	return cuts
}

// assertSplitsEquivalentLong 是长流版本：整口、逐字节与采样切分点
// 结果必须一致；采样点覆盖每个块结构偏移附近。
func assertSplitsEquivalentLong(t *testing.T, stream []byte, want feedResult, offsets []int) {
	t.Helper()
	assertResult(t, []int{len(stream)}, decodeWithSplit(t, stream, []int{len(stream)}), want)
	assertResult(t, repeatOne(len(stream)), decodeWithSplit(t, stream, repeatOne(len(stream))), want)
	for _, cut := range cutCuts(len(stream), offsets) {
		split := []int{cut, len(stream) - cut}
		assertResult(t, split, decodeWithSplit(t, stream, split), want)
	}
}

// structuralOffsets 返回长度覆盖 65535 边界的存储块流中所有
// 块头/LEN/NLEN 关键字节偏移，用于切分点加密采样。
func structuralOffsets(dataLen int) []int {
	var offsets []int
	pos := 2
	for offset := 0; offset < dataLen || (dataLen == 0 && pos == 2); {
		offsets = append(offsets, pos, pos+1, pos+2, pos+3, pos+4)
		if dataLen == 0 {
			break
		}
		end := offset + maxStoredLen
		if end > dataLen {
			end = dataLen
		}
		pos += 5 + (end - offset)
		offset = end
		if offset >= dataLen {
			break
		}
	}
	offsets = append(offsets, pos, pos+1, pos+2, pos+3) // 尾部
	return offsets
}

func sameError(got, want error) bool {
	if want == nil {
		return got == nil
	}
	return errors.Is(got, want)
}

// summarize 为日志生成字节表示：短数据打印完整十六进制，
// 长数据打印长度与首尾片段，避免日志爆炸。
func summarize(b []byte) string {
	if len(b) <= 64 {
		return fmt.Sprintf("%d bytes: %s", len(b), hex.EncodeToString(b))
	}
	return fmt.Sprintf("%d bytes: head=%s...tail=%s",
		len(b), hex.EncodeToString(b[:16]), hex.EncodeToString(b[len(b)-16:]))
}

func TestAdler32KnownVectors(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want uint32
	}{
		{"empty", nil, 0x00000001},
		{"Wikipedia", []byte("Wikipedia"), 0x11E60398},
		{"all-0xff-1", []byte{0xFF}, 0x01000100},
		{"all-0xff-2", []byte{0xFF, 0xFF}, 0x02FF01FF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			whole := adler32(tc.in)
			st := newAdler()
			for _, c := range tc.in {
				st.update([]byte{c})
			}
			bytewise := st.sum()
			t.Logf("判定依据: input=%q whole=0x%08X bytewise=0x%08X want=0x%08X",
				tc.in, whole, bytewise, tc.want)
			if whole != tc.want || bytewise != tc.want {
				t.Fatalf("adler32(%q) = 0x%08X / 0x%08X, want 0x%08X",
					tc.in, whole, bytewise, tc.want)
			}
		})
	}
}

func TestAdler32AllFFModulo(t *testing.T) {
	// 0xFF 字节让 a 以 255 步进，很快越过 65521，专门检验取模。
	// 参考值用规格定义以最朴素的 % 运算逐字节重算，校验条件减法实现。
	for _, n := range []int{1, 2, 256, 257, 65535, 65536, 100000} {
		data := bytes.Repeat([]byte{0xFF}, n)
		got := adler32(data)

		var a, b uint32 = 1, 0
		for range data {
			a = (a + 0xFF) % 65521
			b = (b + a) % 65521
		}
		want := b<<16 | a
		t.Logf("判定依据: n=%d adler=0x%08X reference=0x%08X", n, got, want)
		if got != want {
			t.Fatalf("n=%d adler=0x%08X, want 0x%08X", n, got, want)
		}
	}
}

func TestEmptyEncodingIsExactBytes(t *testing.T) {
	data := []byte{}
	got := encodeStream(t, data, nil)
	want := []byte{0x78, 0x01, 0x01, 0x00, 0x00, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x01}
	t.Logf("判定依据: 空输入输出必须是 头7801|空终块01 0000 FFFF|Adler=00000001；input=%s output=%s",
		summarize(data), summarize(got))
	if !bytes.Equal(got, want) {
		t.Fatalf("empty encoding = % X, want % X", got, want)
	}
	if naive := encodeNaive(data); !bytes.Equal(got, naive) {
		t.Fatalf("stream output % X != naive % X", got, naive)
	}
}

func TestBlockBoundaryLengths(t *testing.T) {
	// 65535：一个非终满块 + 一个 0 字节终块。
	// 65536：一个非终满块 + 一个 1 字节终块。
	for _, n := range []int{65535, 65536} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			data := make([]byte, n)
			for i := range data {
				data[i] = byte(1 + i%251)
			}
			naive := encodeNaive(data)

			remainder := n - maxStoredLen
			wantLen := 2 + (5 + maxStoredLen) + (5 + remainder) + 4
			t.Logf("判定依据: n=%d 结构长度=%d 头2+非终块(5+65535)+终块(5+%d)+尾4；input=%s output=%s",
				n, wantLen, remainder, summarize(data), summarize(naive))
			if len(naive) != wantLen {
				t.Fatalf("n=%d encoded length %d, want %d", n, len(naive), wantLen)
			}
			if naive[2] != 0x00 {
				t.Fatalf("first block must be non-final, got header byte %#04x", naive[2])
			}
			finalOffset := 2 + 5 + maxStoredLen
			if naive[finalOffset] != 0x01 {
				t.Fatalf("second block must be final, got header byte %#04x", naive[finalOffset])
			}
			// 非终块 LEN=65535（ff ff）NLEN=0000；终块 LEN 为 remainder。
			if !bytes.Equal(naive[3:7], []byte{0xFF, 0xFF, 0x00, 0x00}) {
				t.Fatalf("non-final LEN/NLEN = % X", naive[3:7])
			}
			if naive[finalOffset+1] != byte(remainder) || naive[finalOffset+2] != byte(remainder>>8) {
				t.Fatalf("final block LEN != %d", remainder)
			}

			splits := [][]int{{n}, repeatOne(n), repeatSize(n, 65535), repeatSize(n, 65536)}
			for idx, split := range splits {
				got := encodeStream(t, data, split)
				if !bytes.Equal(got, naive) {
					t.Fatalf("split #%d: output differs from naive", idx)
				}
			}
		})
	}
}

func TestEncoderSplitIndependence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	cases := [][]byte{
		nil,
		[]byte("a"),
		bytes.Repeat([]byte{0}, 100),
		bytes.Repeat([]byte{0xFF}, 1000),
	}
	for i := 0; i < 16; i++ {
		n := rng.Intn(140001)
		data := make([]byte, n)
		rng.Read(data)
		cases = append(cases, data)
	}
	for ci, data := range cases {
		naive := encodeNaive(data)
		splits := manySplits(len(data), rand.New(rand.NewSource(int64(ci)+1)))
		for idx, split := range splits {
			got := encodeStream(t, data, split)
			if !bytes.Equal(got, naive) {
				t.Fatalf("case %d (%s), split #%d (%d writes): output differs",
					ci, summarize(data), idx, len(split))
			}
		}
		t.Logf("判定依据: case %d input=%s output=%s；%d 种写切分与朴素整体编码逐字节一致",
			ci, summarize(data), summarize(naive), len(splits))
	}
}

func TestEncoderReplayDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	data := make([]byte, 200000)
	rng.Read(data)
	first := encodeStream(t, data, []int{3, 70000, 65535, 65535, 1})
	second := encodeStream(t, data, repeatOne(len(data)))
	t.Logf("判定依据: 相同输入不同调用重放；first=%s second=%s",
		summarize(first), summarize(second))
	if !bytes.Equal(first, second) {
		t.Fatal("replay produced different bytes")
	}
}

// validStream 构造一条存储块容器流，供解码器测试手工改写。
func validStream(data []byte) []byte { return encodeNaive(data) }

func TestDecodeRoundTripAndSplitIndependence(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	cases := [][]byte{
		nil,
		[]byte("a"),
		[]byte("zlib stored-only container"),
		bytes.Repeat([]byte("Q"), 300),
	}
	for i := 0; i < 12; i++ {
		n := rng.Intn(140001)
		data := make([]byte, n)
		rng.Read(data)
		cases = append(cases, data)
	}
	for ci, data := range cases {
		stream := validStream(data)
		want := feedResult{data: data, delivered: int64(len(data))}
		if len(stream) <= 4000 {
			assertSplitsEquivalent(t, stream, want)
		} else {
			assertSplitsEquivalentLong(t, stream, want, structuralOffsets(len(data)))
		}
		t.Logf("判定依据: case %d input=%s 容器=%s；整口/逐字节与全部（或边界加密采样）切分点结果一致",
			ci, summarize(data), summarize(stream))
	}
}

func TestDecodeBoundaryStreamsSplitSampling(t *testing.T) {
	// 专门覆盖 65535/65536/131070/131071/131072 等块边界，
	// 对每个结构偏移附近的切分点逐一验证结果一致。
	for _, n := range []int{65534, 65535, 65536, 65537, 131070, 131071, 131072} {
		data := make([]byte, n)
		for i := range data {
			data[i] = byte(i * 7)
		}
		stream := validStream(data)
		want := feedResult{data: data, delivered: int64(n)}
		assertSplitsEquivalentLong(t, stream, want, structuralOffsets(n))
		t.Logf("判定依据: n=%d 容器长度=%d，块头/LEN/NLEN/尾部附近 ±8 及均匀采样切分点全部一致",
			n, len(stream))
	}
}

func TestDecodeEmptyExact(t *testing.T) {
	stream := []byte{0x78, 0x01, 0x01, 0x00, 0x00, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x01}
	res := decodeWithSplit(t, stream, nil) // nil split => 一次喂入
	t.Logf("判定依据: 空容器流一次喂入 output=%v delivered=%d err=%v",
		res.data, res.delivered, res.err)
	if res.err != nil || len(res.data) != 0 || res.delivered != 0 {
		t.Fatalf("empty stream: %+v", res)
	}
	assertSplitsEquivalent(t, stream, feedResult{})
}

// headerCase 描述头部四错误的按序检测。
func TestDecodeHeaderErrorOrder(t *testing.T) {
	cases := []struct {
		name   string
		header []byte
		want   error
	}{
		// 1) CM != 8 最先报，即使其他字段也坏。
		{"method-not-8", []byte{0x77, 0x01}, ErrBadMethod},
		{"method-wins-over-all", []byte{0xF7, 0xFF}, ErrBadMethod},
		// 2) CM=8 后 CINFO>7 报窗口错误（78 01 本身可整除，改 CMF 高 4 位
		//    需要同步给出能整除但带 FDICT 的 FLG，验证窗口优先于字典）。
		{"window-too-large", cinfoHeader(8, 0x00), ErrBadWindow},
		// 3) CM/CINFO 合法后检查 31 整除。
		{"check-value", []byte{0x78, 0x00}, ErrBadCheckValue},
		// 0x7860 带 FDICT=1 且不可被 31 整除：必须先报整除错误。
		{"check-before-dict", []byte{0x78, 0x60}, ErrBadCheckValue},
		// 4) 整除但 FDICT 置位（0x7820 恰好可被 31 整除）。
		{"dict-flag", []byte{0x78, 0x20}, ErrDictionaryPresent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 头后补充合法终块与尾部；错误必须在读到头部时立即触发，
			// 与后续字节无关。
			stream := append(append([]byte{}, tc.header...),
				0x01, 0x00, 0x00, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x01)
			want := feedResult{err: tc.want}
			assertSplitsEquivalent(t, stream, want)
			res := decodeWithSplit(t, stream, nil)
			t.Logf("判定依据: %s header=%02X -> err=%v（期望首个错误 %v）",
				tc.name, tc.header, res.err, tc.want)
		})
	}
}

// cinfoHeader 构造 CM=8、CINFO=cinfo 的头部，FLG 使整除成立但可带 FDICT。
func cinfoHeader(cinfo int, extraFLG byte) []byte {
	cmf := byte(cinfo<<4) | 8
	flg := byte(0)
	for ; flg != 0xFF; flg++ {
		if (uint16(cmf)<<8|uint16(flg|extraFLG))%31 == 0 {
			return []byte{cmf, flg | extraFLG}
		}
	}
	panic("no FCHECK found")
}

func TestDecodeBlockHeaderErrors(t *testing.T) {
	// 合法头之后改写块头字节（偏移 2），构造四类块错误。
	base := validStream([]byte("xyz"))
	cases := []struct {
		name string
		bh   byte
		want error
	}{
		{"reserved-high-bits", 0x08, ErrBlockHeaderReserved},
		{"reserved-high-bits-final", 0xF9, ErrBlockHeaderReserved},
		{"fixed-huffman", 0x02, ErrBlockFixedHuffman},
		{"fixed-huffman-final", 0x03, ErrBlockFixedHuffman},
		{"dynamic-huffman", 0x04, ErrBlockDynamicHuffman},
		{"dynamic-huffman-final", 0x05, ErrBlockDynamicHuffman},
		{"reserved-type", 0x06, ErrBlockReservedType},
		{"reserved-type-final", 0x07, ErrBlockReservedType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stream := append([]byte{}, base...)
			stream[2] = tc.bh
			assertSplitsEquivalent(t, stream, feedResult{err: tc.want})
			res := decodeWithSplit(t, stream, nil)
			t.Logf("判定依据: %s blockHeader=%08b -> err=%v", tc.name, tc.bh, res.err)
		})
	}
}

func TestDecodeBadNLEN(t *testing.T) {
	stream := validStream([]byte("xy"))
	// LEN=0002，NLEN 应为 FFFD；篡改 NLEN 低字节。
	if stream[3] != 0x02 || stream[5] != 0xFD {
		t.Fatalf("unexpected base layout: % X", stream[:9])
	}
	stream[6] = 0xFC
	res := decodeWithSplit(t, stream, nil)
	t.Logf("判定依据: 篡改 NLEN 为 LEN 的非补码 input=%s -> err=%v",
		summarize(stream), res.err)
	if !errors.Is(res.err, ErrBadNLEN) {
		t.Fatalf("want ErrBadNLEN, got %v", res.err)
	}
	assertSplitsEquivalent(t, stream, feedResult{err: ErrBadNLEN})

	// 非终块 LEN=0 合法：手工构造 非终空块 + 终空块。
	zeroNonFinal := []byte{
		0x78, 0x01,
		0x00, 0x00, 0x00, 0xFF, 0xFF, // BFINAL=0, LEN=0
		0x01, 0x00, 0x00, 0xFF, 0xFF, // BFINAL=1, LEN=0
		0x00, 0x00, 0x00, 0x01,
	}
	res = decodeWithSplit(t, zeroNonFinal, nil)
	t.Logf("判定依据: 非终空块必须被接受 output=%v err=%v", res.data, res.err)
	if res.err != nil || len(res.data) != 0 {
		t.Fatalf("zero-length non-final block rejected: %+v", res)
	}
	assertSplitsEquivalent(t, zeroNonFinal, feedResult{})
}

func TestDecodeTrailerErrors(t *testing.T) {
	data := []byte("trailer check")

	// 尾部之后再有字节 -> ErrTrailingBytes（此时数据已全部交付）。
	extra := append(validStream(data), 0x42)
	res := decodeWithSplit(t, extra, nil)
	t.Logf("判定依据: 尾部后追加 1 字节 -> delivered=%d err=%v", res.delivered, res.err)
	if !errors.Is(res.err, ErrTrailingBytes) || res.delivered != int64(len(data)) {
		t.Fatalf("extra byte: %+v", res)
	}
	assertSplitsEquivalent(t, extra, feedResult{data: data, delivered: int64(len(data)), err: ErrTrailingBytes})

	// 尾部 Adler 错误 -> ErrChecksum。
	bad := validStream(data)
	bad[len(bad)-1] ^= 0xFF
	res = decodeWithSplit(t, bad, nil)
	t.Logf("判定依据: 末字节翻转 -> delivered=%d err=%v（数据仍全部交付）", res.delivered, res.err)
	if !errors.Is(res.err, ErrChecksum) || res.delivered != int64(len(data)) {
		t.Fatalf("bad checksum: %+v", res)
	}
	assertSplitsEquivalent(t, bad, feedResult{data: data, delivered: int64(len(data)), err: ErrChecksum})
}

func TestDecodeTruncation(t *testing.T) {
	stream := validStream([]byte("abcdefghij"))
	// 每个前缀都必须在 Close 时得到 ErrTruncated，且与切分无关。
	for n := 0; n < len(stream); n++ {
		prefix := stream[:n]
		var wantData []byte
		var wantDelivered int64
		// 计算在该截断点之前合法交付的数据，作为各切分的共同指纹。
		ref := decodeWithSplit(t, prefix, nil)
		wantData, wantDelivered = ref.data, ref.delivered
		if !errors.Is(ref.err, ErrTruncated) {
			t.Fatalf("prefix len %d: want ErrTruncated, got %v", n, ref.err)
		}
		assertSplitsEquivalent(t, prefix, feedResult{data: wantData, delivered: wantDelivered, err: ErrTruncated})
	}
	t.Logf("判定依据: 全部 %d 个截断前缀 Close 均报 ErrTruncated，且任意切分指纹一致", len(stream))
}

func TestDecoderPoisoned(t *testing.T) {
	bad := validStream([]byte("abc"))
	bad[len(bad)-1] ^= 0xFF // 尾部校验失败

	dec := NewDecoder()
	out, err := dec.Write(bad)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("first Write: %v", err)
	}
	deliveredAfter := dec.Delivered()
	if deliveredAfter != 3 || !bytes.Equal(out, []byte("abc")) {
		t.Fatalf("data should be delivered before trailer check: out=%q n=%d", out, deliveredAfter)
	}

	// 出错后所有调用粘滞：ErrPoisoned 包装首错误，状态与计数不再变化。
	for i := 0; i < 3; i++ {
		out, err = dec.Write([]byte{0, 1, 2, 3})
		if !errors.Is(err, ErrPoisoned) || errors.Is(err, ErrChecksum) || len(out) != 0 {
			t.Fatalf("post-error Write #%d: %v out=%x", i, err, out)
		}
		if err := dec.Close(); !errors.Is(err, ErrPoisoned) {
			t.Fatalf("post-error Close: %v", err)
		}
	}
	if dec.Delivered() != deliveredAfter {
		t.Fatalf("Delivered changed after poison: %d -> %d", deliveredAfter, dec.Delivered())
	}
	t.Logf("判定依据: 首个错误 ErrChecksum 后，Write/Close 均为 ErrPoisoned 且 Delivered 粘滞在 %d", deliveredAfter)
}

func TestEncoderCloseRejectsFurtherCalls(t *testing.T) {
	var out bytes.Buffer
	enc := NewEncoder(&out)
	if _, err := enc.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := append([]byte{}, out.Bytes()...)

	n, err := enc.Write([]byte("more"))
	if n != 0 || !errors.Is(err, ErrClosed) {
		t.Fatalf("Write after Close: n=%d err=%v", n, err)
	}
	if err := enc.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("second Close: %v", err)
	}
	if err := enc.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("third Close: %v", err)
	}
	if !bytes.Equal(out.Bytes(), snapshot) {
		t.Fatal("rejected calls changed encoder output")
	}
	t.Logf("判定依据: Close 后 Write/再Close 返回 ErrClosed 且输出不变 output=%s", summarize(snapshot))
}

// errWriter 在一定字节数后开始失败，用于验证编码器的粘滞失败态。
type errWriter struct{ remaining int }

func (w *errWriter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, errors.New("disk on fire")
	}
	n := len(p)
	if n > w.remaining {
		n = w.remaining
	}
	w.remaining -= n
	if n < len(p) {
		return n, errors.New("disk on fire")
	}
	return n, nil
}

func TestEncoderWriterFailurePoisoned(t *testing.T) {
	enc := NewEncoder(&errWriter{remaining: 2})
	_, err := enc.Write(bytes.Repeat([]byte{1}, 100000))
	if !errors.Is(err, ErrWriter) {
		t.Fatalf("want ErrWriter, got %v", err)
	}
	if _, err := enc.Write([]byte{1}); !errors.Is(err, ErrWriter) {
		t.Fatalf("Write after failure: %v", err)
	}
	if err := enc.Close(); !errors.Is(err, ErrWriter) {
		t.Fatalf("Close after failure: %v", err)
	}
	t.Logf("判定依据: 底层 writer 失败后所有后续调用返回 ErrWriter: %v", err)
}

func TestEncoderFailureAtHeaderAndClose(t *testing.T) {
	// 头部 2 字节都写不出去：Write 与之后的 Close 都应粘滞为 ErrWriter。
	enc := NewEncoder(&errWriter{remaining: 0})
	if _, err := enc.Write([]byte{1}); !errors.Is(err, ErrWriter) {
		t.Fatalf("header Write: %v", err)
	}
	if err := enc.Close(); !errors.Is(err, ErrWriter) {
		t.Fatalf("Close after header failure: %v", err)
	}

	// 头部写出成功、数据缓冲成功，但 Close 输出终块时底层失败。
	enc2 := NewEncoder(&errWriter{remaining: 2})
	if _, err := enc2.Write(bytes.Repeat([]byte{9}, 3)); err != nil {
		t.Fatalf("buffered Write should not hit writer: %v", err)
	}
	if err := enc2.Close(); !errors.Is(err, ErrWriter) {
		t.Fatalf("Close flush failure: %v", err)
	}
	if err := enc2.Close(); !errors.Is(err, ErrWriter) {
		t.Fatalf("Close after failure should stay ErrWriter, got %v", err)
	}
	t.Log("判定依据: 头部写失败与 Close 刷块失败都进入 ErrWriter 粘滞态")
}

func TestDecoderStreamingDelivery(t *testing.T) {
	data := bytes.Repeat([]byte{0x5A}, 300)
	stream := validStream(data)
	dec := NewDecoder()
	// 只喂入块头 + LEN/NLEN + 10 个数据字节，必须立刻交付 10 字节，
	// 即使后续字节尚未到达。
	out, err := dec.Write(stream[:7+10])
	if err != nil || len(out) != 10 || dec.Delivered() != 10 {
		t.Fatalf("early delivery: out=%d delivered=%d err=%v", len(out), dec.Delivered(), err)
	}
	out, err = dec.Write(stream[7+10:])
	if err != nil || len(out) != 290 || dec.Delivered() != 300 || !bytes.Equal(out, data[10:]) {
		t.Fatalf("rest delivery: out=%d delivered=%d err=%v", len(out), dec.Delivered(), err)
	}
	if err := dec.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("判定依据: 块数据边收边交付，首段 10 字节在尾部到达前已交付，最终 delivered=%d", dec.Delivered())
}

func TestEncoderConcurrentWrites(t *testing.T) {
	const total = 200000
	const writers = 8
	const perWrite = 1000

	var out bytes.Buffer
	enc := NewEncoder(&out)
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			// 每个 goroutine 写同值字节；输出是否等价于某个串行顺序，
			// 通过“长度确定 + 可被解码器无损还原为 total 个同值字节”验证。
			p := bytes.Repeat([]byte{byte(0x30 + id)}, perWrite)
			_ = p
			for i := 0; i < total/writers/perWrite; i++ {
				chunk := make([]byte, perWrite)
				for j := range chunk {
					chunk[j] = 0x41 // 全部同值，串行化顺序不影响内容
				}
				if _, err := enc.Write(chunk); err != nil {
					t.Errorf("concurrent Write: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}

	encoded := out.Bytes()
	res := decodeWithSplit(t, encoded, repeatOne(len(encoded)))
	want := bytes.Repeat([]byte{0x41}, total)
	t.Logf("判定依据: %d 个 goroutine 并发写 %d 字节，输出长度=%d，解码无损且 err=%v",
		writers, total, len(encoded), res.err)
	if res.err != nil || !bytes.Equal(res.data, want) || res.delivered != total {
		t.Fatalf("concurrent encode: err=%v delivered=%d/%d", res.err, res.delivered, total)
	}
	if naive := encodeNaive(want); !bytes.Equal(encoded, naive) {
		t.Fatal("concurrent output differs byte-for-byte from serial expectation")
	}
}

func TestDecoderConcurrentWrites(t *testing.T) {
	const total = 200000
	data := bytes.Repeat([]byte{0x77}, total)
	stream := encodeNaive(data)

	segments := make([][]byte, 0)
	for i := 0; i < len(stream); i += 997 {
		end := i + 997
		if end > len(stream) {
			end = len(stream)
		}
		segments = append(segments, stream[i:end])
	}
	// 每个解码器：每个段一个 goroutine，用 per-idx 闸门保证完成顺序
	// 严格等于流顺序（段 0..i-1 全部 Write 成功后段 i 才能动手），
	// 因而大量 goroutine 在闸门与互斥锁上真实竞争，但效果等价于串行；
	// 同时再跑若干只读 Delivered 的 goroutine 增加读写交错。
	const decCount = 8
	decs := make([]*Decoder, decCount)
	var feeders sync.WaitGroup
	var readers sync.WaitGroup
	errCh := make(chan error, decCount*len(segments))
	stop := make(chan struct{})
	for d := range decs {
		decs[d] = NewDecoder()
		dec := decs[d]
		gates := make([]chan struct{}, len(segments))
		for i := range gates {
			gates[i] = make(chan struct{})
		}
		for i, seg := range segments {
			i, seg := i, seg
			feeders.Add(1)
			go func() {
				defer feeders.Done()
				if i > 0 {
					<-gates[i-1]
				}
				if _, err := dec.Write(seg); err != nil {
					errCh <- err
				}
				close(gates[i])
			}()
		}
		for r := 0; r < 4; r++ {
			readers.Add(1)
			go func() {
				defer readers.Done()
				for {
					select {
					case <-stop:
						return
					default:
						_ = dec.Delivered()
					}
				}
			}()
		}
	}
	feeders.Wait()
	close(stop)
	readers.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent decode Write: %v", err)
		}
	}
	for d, dec := range decs {
		if err := dec.Close(); err != nil {
			t.Fatalf("decoder %d Close: %v", d, err)
		}
		if dec.Delivered() != total {
			t.Fatalf("decoder %d delivered %d, want %d", d, dec.Delivered(), total)
		}
	}
	t.Logf("判定依据: %d 个解码器、%d 个有序闸门 goroutine + 并发 Delivered 读，全部 delivered=%d",
		decCount, len(segments), total)
}

func TestInteropWithStandardLibrary(t *testing.T) {
	// 额外互通性证据：标准库 compress/zlib 必须能解码本编码器的输出，
	// 本解码器也能解码标准库的存储级输出（通过 zero 压缩级别的构造）。
	// 这里只验证 stdlib 解码我们的流，确保字节级布局符合 RFC。
	rng := rand.New(rand.NewSource(2026))
	for _, n := range []int{0, 1, 65535, 65536, 131071} {
		data := make([]byte, n)
		rng.Read(data)
		split := append([]int{333}, repeatSize(n-333, 70000)...)
		if n == 0 {
			split = nil
		}
		encoded := encodeStream(t, data, split)
		zr, err := zlibNewReader(bytes.NewReader(encoded))
		if err != nil {
			t.Fatalf("n=%d stdlib reader: %v", n, err)
		}
		got, err := zlibReadAll(zr)
		if err != nil {
			t.Fatalf("n=%d stdlib decode: %v", n, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("n=%d stdlib roundtrip mismatch", n)
		}
		t.Logf("判定依据: n=%d 本容器输出可被标准库 compress/zlib 解码", n)
	}
}
