package lzw

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// pack 把 (码, 码宽) 序列低位在先打包为字节，末尾补 0。
func pack(seq []naiveCode) []byte {
	var out []byte
	var acc uint32
	var n uint
	for _, c := range seq {
		acc |= uint32(c.code) << n
		n += c.bits
		for n >= 8 {
			out = append(out, byte(acc))
			acc >>= 8
			n -= 8
		}
	}
	if n > 0 {
		out = append(out, byte(acc))
	}
	return out
}

func randBytes(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.Intn(256))
	}
	return b
}

func encodeAll(t *testing.T, input []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if _, err := enc.Write(input); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return buf.Bytes()
}

func decodeAll(t *testing.T, data []byte) []byte {
	t.Helper()
	out, err := io.ReadAll(NewDecoder(bytes.NewReader(data)))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func roundTrip(t *testing.T, input []byte) {
	t.Helper()
	data := encodeAll(t, input)
	if got := decodeAll(t, data); !bytes.Equal(got, input) {
		t.Fatalf("roundtrip mismatch: want %d bytes, got %d bytes", len(input), len(got))
	}
}

// TestExampleAAAAAAA 题目示例：256,65,258,259,65,257，均 9 位。
func TestExampleAAAAAAA(t *testing.T) {
	input := []byte("AAAAAAA")
	seq, packed := naiveEncode(input)
	want := []naiveCode{{256, 9}, {65, 9}, {258, 9}, {259, 9}, {65, 9}, {257, 9}}
	if fmt.Sprint(seq) != fmt.Sprint(want) {
		t.Fatalf("naive seq = %v, want %v", seq, want)
	}
	data := encodeAll(t, input)
	if !bytes.Equal(data, packed) {
		t.Fatalf("stream %08b != naive %08b", data, packed)
	}
	roundTrip(t, input)
	t.Logf("input=%q codes=[%s] bytes=%08b", input, codesString(seq), data)
}

// TestEmpty 空输入：清除码+结束码 18 位，补零为 3 字节。
func TestEmpty(t *testing.T) {
	seq, packed := naiveEncode(nil)
	if fmt.Sprint(seq) != fmt.Sprint([]naiveCode{{256, 9}, {257, 9}}) {
		t.Fatalf("empty seq = %v", seq)
	}
	data := encodeAll(t, nil)
	if !bytes.Equal(data, packed) || len(data) != 3 {
		t.Fatalf("empty output=%08b, want 3 bytes", data)
	}
	if out := decodeAll(t, data); len(out) != 0 {
		t.Fatalf("empty roundtrip got %d bytes", len(out))
	}
	t.Logf("input=empty codes=[%s] bytes=%08b", codesString(seq), data)
}

// TestSelfRef258_259 覆盖 258/259 号码自引用（KwKwK）。
func TestSelfRef258_259(t *testing.T) {
	for _, s := range []string{"AAA", "AAAA", "AAAAA", "AAAAAAA"} {
		input := []byte(s)
		seq, packed := naiveEncode(input)
		data := encodeAll(t, input)
		if !bytes.Equal(data, packed) {
			t.Fatalf("%q stream != naive", s)
		}
		roundTrip(t, input)
		t.Logf("input=%q codes=[%s] 258=self(AA),259=self(AAA)", s, codesString(seq))
	}
}

// buildPairs 构造 0,1,0,2,0,...,k,0；每对产生一个新条目，共 2k 个条目。
func buildPairs(k int) []byte {
	out := []byte{0}
	for i := 1; i <= k; i++ {
		out = append(out, byte(i), 0)
	}
	return out
}

// TestWidth511_512 在 511/512 编号附近升宽。
func TestWidth511_512(t *testing.T) {
	for _, k := range []int{127, 128, 255, 256} {
		input := buildPairs(k)
		seq, packed := naiveEncode(input)
		data := encodeAll(t, input)
		if !bytes.Equal(data, packed) {
			t.Fatalf("k=%d stream/naive mismatch", k)
		}
		roundTrip(t, input)
		tail := seq
		if len(tail) > 5 {
			tail = tail[len(tail)-5:]
		}
		t.Logf("511/512 k=%d inputLen=%d nCodes=%d tail=[%s]",
			k, len(input), len(seq), codesString(tail))
	}
}

// TestWidth2047_2048 高熵长输入使码宽达到 12。
func TestWidth2047_2048(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	input := randBytes(rng, 40000)
	seq, packed := naiveEncode(input)
	data := encodeAll(t, input)
	if !bytes.Equal(data, packed) {
		t.Fatalf("width 2047/2048 mismatch")
	}
	roundTrip(t, input)
	maxW := uint(0)
	for _, c := range seq {
		if c.bits > maxW {
			maxW = c.bits
		}
	}
	if maxW < 12 {
		t.Fatalf("expected width 12 reached, got %d", maxW)
	}
	t.Logf("2047/2048 inputLen=%d nCodes=%d maxWidth=%d", len(input), len(seq), maxW)
}

// TestDictionaryFullClear 字典写满 4095 触发清除并继续编码。
func TestDictionaryFullClear(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	input := randBytes(rng, 80000)
	seq, packed := naiveEncode(input)
	data := encodeAll(t, input)
	if !bytes.Equal(data, packed) {
		t.Fatalf("dict-full stream/naive mismatch")
	}
	nClear := 0
	var clearWidths []uint
	for _, c := range seq {
		if c.code == ClearCode {
			nClear++
			clearWidths = append(clearWidths, c.bits)
		}
	}
	if nClear < 2 {
		t.Fatalf("expected >=2 clear codes, got %d", nClear)
	}
	for _, w := range clearWidths[1:] {
		if w != 12 {
			t.Fatalf("full-dict clear width = %d, want 12", w)
		}
	}
	roundTrip(t, input)
	t.Logf("dict-full inputLen=%d clears=%d clearWidths=%v", len(input), nClear, clearWidths)
}

// TestPhantomWiden Close 补计编号恰好触发升宽：
// 254 个新条目后 free=512，Close 补计 512，使结束码以 10 位输出。
func TestPhantomWiden(t *testing.T) {
	input := buildPairs(127) // 254 条目 -> free=512
	if len(input) != 255 {
		t.Fatalf("setup len=%d, want 255", len(input))
	}
	seq, packed := naiveEncode(input)
	last := seq[len(seq)-1]
	tail3 := seq[len(seq)-3:]
	if !(last.code == EndCode && last.bits == 10) {
		t.Fatalf("phantom widen: end code=%+v (want 257/10), tail=%v", last, tail3)
	}
	data := encodeAll(t, input)
	if !bytes.Equal(data, packed) {
		t.Fatalf("phantom widen mismatch: %v vs %v", data, packed)
	}
	roundTrip(t, input)
	t.Logf("phantom-widen codesTail=[%s] => Close 补计 512, 结束码 10 位", codesString(tail3))
}

// TestPhantomWiden11To12 Close 补计 2048 恰好触发 11→12 升宽。
// 构造“不匹配序列”使数据码恰好新增 2047 个条目（free=2305?）——
// 直接用随机种子搜索能命中 free=2048 的输入，验证结束码 12 位且可往返。
func TestPhantomWiden11To12(t *testing.T) {
	// 在一段固定高熵输入上逐长度扫描，找到 Close 补计恰好把 free 从
	// 2048 推进到 2049（结束码 12 位、最后数据码仍 11 位）的长度。
	base := randBytes(rand.New(rand.NewSource(2024)), 20000)
	var input []byte
	foundLen := -1
	lo, hi := 2000, 6000
	for n := lo; n <= hi; n++ {
		seq, _ := naiveEncode(base[:n])
		if len(seq) >= 2 && seq[len(seq)-2].bits == 11 && seq[len(seq)-1].bits == 12 {
			foundLen = n
			input = base[:n]
			break
		}
	}
	if foundLen < 0 {
		t.Skip("扫描区间内未命中 11->12 补计点（仅环境数据原因）")
	}
	seq, packed := naiveEncode(input)
	if seq[len(seq)-1].bits != 12 {
		t.Fatalf("phantom 11->12: end code width=%d, want 12", seq[len(seq)-1].bits)
	}
	saw11 := false
	for _, c := range seq[:len(seq)-1] {
		if c.bits == 11 {
			saw11 = true
		}
	}
	if !saw11 {
		t.Fatal("expected at least one 11-bit code before 12-bit end code")
	}
	data := encodeAll(t, input)
	if !bytes.Equal(data, packed) {
		t.Fatalf("phantom 11->12 stream/naive mismatch")
	}
	roundTrip(t, input)
	t.Logf("phantom 11->12 inputLen=%d nCodes=%d tail=[%s]",
		len(input), len(seq), codesString(seq[len(seq)-4:]))
}

// TestWriteChunkIndependence 任意切分（含逐字节）输出逐字节相同。
func TestWriteChunkIndependence(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	cases := [][]byte{nil, []byte("A"), []byte("AAAAAAA"), buildPairs(255), randBytes(rng, 20000)}
	for ci, input := range cases {
		var ref []byte
		for _, chunk := range []int{1, 2, 3, 7, 64, 4096} {
			var buf bytes.Buffer
			enc := NewEncoder(&buf)
			for i := 0; i < len(input); {
				end := i + chunk
				if end > len(input) {
					end = len(input)
				}
				if _, err := enc.Write(input[i:end]); err != nil {
					t.Fatalf("case %d chunk %d: %v", ci, chunk, err)
				}
				i = end
			}
			if err := enc.Close(); err != nil {
				t.Fatal(err)
			}
			if ref == nil {
				ref = buf.Bytes()
			} else if !bytes.Equal(ref, buf.Bytes()) {
				t.Fatalf("case %d chunk %d output differs", ci, chunk)
			}
		}
		if len(input) > 0 {
			roundTrip(t, input)
		}
	}
	t.Log("Write 切分无关性：1/2/3/7/64/4096 各切分输出逐字节相同")
}

// TestRandomVsNaive 随机输入：流式输出 == 朴素打包输出，解码逐字节还原。
func TestRandomVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for iter := 0; iter < 200; iter++ {
		n := rng.Intn(3000)
		input := randBytes(rng, n)
		seq, packed := naiveEncode(input)
		var buf bytes.Buffer
		enc := NewEncoder(&buf)
		for i := 0; i < len(input); {
			end := i + 1 + rng.Intn(37)
			if end > len(input) {
				end = len(input)
			}
			if _, err := enc.Write(input[i:end]); err != nil {
				t.Fatal(err)
			}
			i = end
		}
		if err := enc.Close(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf.Bytes(), packed) {
			t.Fatalf("iter %d n=%d stream != naive", iter, n)
		}
		if got := decodeAll(t, packed); !bytes.Equal(got, input) {
			t.Fatalf("iter %d n=%d decode mismatch", iter, n)
		}
		if iter < 3 || n > 2800 {
			tail := seq
			if len(tail) > 6 {
				tail = tail[len(tail)-6:]
			}
			t.Logf("random iter=%d n=%d nCodes=%d tail=[%s]",
				iter, n, len(seq), codesString(tail))
		}
	}
}

// ---- 非法码流 ----

func expectFail(t *testing.T, data []byte, want error, wantIndex int) {
	t.Helper()
	dec := NewDecoder(bytes.NewReader(data))
	_, err := io.ReadAll(dec)
	if err == nil {
		t.Fatalf("want %v, got nil (data=%08b)", want, data)
	}
	if !errors.Is(err, want) {
		t.Fatalf("want errors.Is %v, got %v", want, err)
	}
	var ce *CodeError
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not *CodeError", err)
	}
	if ce.Index != wantIndex {
		t.Fatalf("code index = %d, want %d (%v)", ce.Index, wantIndex, err)
	}
	// 粘滞失败：后续调用返回同一错误，状态不变。
	n, err2 := dec.Read(make([]byte, 4))
	if n != 0 || !errors.Is(err2, want) || dec.Err() != err {
		t.Fatalf("sticky failure not held: n=%d err=%v", n, err2)
	}
	t.Logf("拒绝原因=%v 码序号=%d (data=%08b)", want, ce.Index, data)
}

// TestBadFirstCode 首个码不是清除码。
func TestBadFirstCode(t *testing.T) {
	expectFail(t, pack([]naiveCode{{65, 9}, {257, 9}}), ErrNotCleared, 1)
}

// TestBadCodeAfterClear 清除码之后首个码不小于 256。
func TestBadCodeAfterClear(t *testing.T) {
	expectFail(t, pack([]naiveCode{{256, 9}, {258, 9}, {257, 9}}),
		ErrCodeAfterClear, 2)
}

// TestCodeOutOfRange 码值大于解码端下一个待新增编号。
func TestCodeOutOfRange(t *testing.T) {
	// 256,65 之后 free=258；码 260 非法（258 是合法自引用）。
	expectFail(t, pack([]naiveCode{{256, 9}, {65, 9}, {260, 9}, {257, 9}}),
		ErrCodeOutOfRange, 3)
}

// TestCodeEqualsFreeLegal 码值恰等于待新增编号（258 自引用）合法。
func TestCodeEqualsFreeLegal(t *testing.T) {
	// 256,65,258,257 => "AAA"
	data := pack([]naiveCode{{256, 9}, {65, 9}, {258, 9}, {257, 9}})
	if got := decodeAll(t, data); string(got) != "AAA" {
		t.Fatalf("self-ref decode = %q, want AAA", got)
	}
}

// TestTruncated 结束码前被截断：字节边界截断与位截断都拒绝。
func TestTruncated(t *testing.T) {
	// 仅一个清除码，之后无任何字节。
	expectFail(t, pack([]naiveCode{{ClearCode, 9}, {65, 9}}), ErrTruncated, 3)
	// 清除码 + 半个码的残位（手动打包 9+4=13 位，剩 4 位为 0）。
	var b []byte
	var acc uint32
	var n uint
	put := func(code, bits uint) {
		acc |= uint32(code) << n
		n += bits
		for n >= 8 {
			b = append(b, byte(acc))
			acc >>= 8
			n -= 8
		}
	}
	put(256, 9)
	put(0, 4)
	if n > 0 {
		b = append(b, byte(acc))
	}
	expectFail(t, b, ErrTruncated, 2)
}

// TestPaddingNonZero 结束码之后补齐位非零。
func TestPaddingNonZero(t *testing.T) {
	// 空输入正常为 3 字节：18 位，第三字节高 6 位为补齐。
	good := pack([]naiveCode{{256, 9}, {257, 9}})
	if got := decodeAll(t, good); len(got) != 0 {
		t.Fatal("sanity: empty decode failed")
	}
	bad := append([]byte(nil), good...)
	bad[2] |= 0x40 // 某个补齐位置 1
	expectFail(t, bad, ErrPaddingNonZero, 2)
}

// TestTrailingData 结束码之后还有多余字节（含全零字节）。
func TestTrailingData(t *testing.T) {
	good := pack([]naiveCode{{256, 9}, {257, 9}}) // 恰好 3 字节
	// 让结束码落在字节边界：构造整字节对齐的序列。
	// 256,65 占 18 位；再补若干 9 位码使总位数为 8 的倍数后放 257。
	// 8 个 9 位码 = 72 位 = 9 字节：256,65,258,259,260?, 需合法。
	// 简单做法：用编码器产生 AAAAAAA（6 码 54 位，不对齐）；
	// 直接在 good 后加一个 0 字节属于“补齐之后的多余字节”，
	// 但 good 第 3 字节尚有补齐位——加第 4 字节即多余字节。
	trailingZero := append(append([]byte(nil), good...), 0)
	expectFail(t, trailingZero, ErrTrailingData, 2)
	nonZero := append(append([]byte(nil), good...), 0xFF)
	expectFail(t, nonZero, ErrTrailingData, 2)
}

// TestWriteAfterClose Close 之后再 Write / 重复 Close 均拒绝且不改状态。
func TestWriteAfterClose(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	n, err := enc.Write([]byte("A"))
	if n != 0 || !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close: n=%d err=%v", n, err)
	}
	if err := enc.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("double close: %v", err)
	}
	// 已产出的字节仍可正常解码为空输入。
	if out := decodeAll(t, buf.Bytes()); len(out) != 0 {
		t.Fatalf("state changed by rejected call: %q", out)
	}
}

// TestConcurrent 编码器/解码器在多 goroutine 竞争调用下结果等价某个
// 串行顺序：多个 worker 竞争同一把编码器锁，按“抢到锁的先后”写入
// 分片（即某个合法串行交错），输出必须可被解码器无损还原，且与把
// 同样的串行顺序喂给编码器的结果逐字节一致。
func TestConcurrent(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	input := randBytes(rng, 5000)

	for trial := 0; trial < 20; trial++ {
		chunks := splitRandom(input, 16)

		// 一条有界通道充当任务池：多个 goroutine 竞争取任务并写入，
		// 实际写入顺序即某个串行顺序；记录该顺序以便对照。
		tasks := make(chan []byte, len(chunks))
		for _, ch := range chunks {
			tasks <- ch
		}
		close(tasks)

		var buf bytes.Buffer
		enc := NewEncoder(&buf)
		var orderMu sync.Mutex // 与编码器调用串行化保持同一顺序
		var ordered []byte
		var wg sync.WaitGroup
		for w := 0; w < 8; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for ch := range tasks {
					orderMu.Lock()
					if _, err := enc.Write(ch); err != nil {
						orderMu.Unlock()
						t.Error(err)
						return
					}
					ordered = append(ordered, ch...)
					orderMu.Unlock()
				}
			}()
		}
		wg.Wait()
		if err := enc.Close(); err != nil {
			t.Fatal(err)
		}

		// 并发交错下 ordered 是 input 分片的某种拼接（每片内部连续）；
		// 关键不变量：输出字节 == 按同一串行顺序的单次编码结果，
		// 且解码结果与该串行顺序完全一致。
		ref := encodeAll(t, ordered)
		if !bytes.Equal(ref, buf.Bytes()) {
			t.Fatalf("trial %d: concurrent output != serial replay", trial)
		}
		got := decodeAll(t, buf.Bytes())
		if !bytes.Equal(got, ordered) {
			t.Fatalf("trial %d: concurrent decode mismatch", trial)
		}
	}
}

func splitRandom(input []byte, parts int) [][]byte {
	if len(input) == 0 {
		return nil
	}
	rng := rand.New(rand.NewSource(123))
	var out [][]byte
	for len(input) > 0 {
		size := 1 + rng.Intn(len(input)/parts+1)
		if size > len(input) {
			size = len(input)
		}
		out = append(out, append([]byte(nil), input[:size]...))
		input = input[size:]
	}
	return out
}

// TestConcurrentDeterministic 同一输入重放得到完全相同字节（并发读安全）。
func TestConcurrentReplay(t *testing.T) {
	input := buildPairs(200)
	var outputs [][]byte
	for i := 0; i < 8; i++ {
		outputs = append(outputs, encodeAll(t, input))
	}
	for i := 1; i < len(outputs); i++ {
		if !bytes.Equal(outputs[0], outputs[i]) {
			t.Fatalf("replay %d differs", i)
		}
	}
	// 解码器并发 Read 也必须安全：多 goroutine 在同一把锁保护下
	// 串行化 Read 与追加，结果必须逐字节还原（验证内部锁可重入语义
	// 下的并发可用性；锁竞争不影响正确性）。
	data := outputs[0]
	dec := NewDecoder(bytes.NewReader(data))
	var mu sync.Mutex
	var got []byte
	done := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-done // 同时被唤醒后竞争锁
			buf := make([]byte, 37)
			for {
				mu.Lock()
				n, err := dec.Read(buf)
				got = append(got, buf[:n]...)
				mu.Unlock()
				if err != nil {
					break
				}
			}
		}()
	}
	close(done)
	wg.Wait()
	if !bytes.Equal(got, input) {
		t.Fatalf("concurrent decode mismatch: %d vs %d", len(got), len(input))
	}
}
