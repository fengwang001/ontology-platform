package frame

import (
	"bytes"
	"sync"
	"testing"
)

// incompressible 返回 n 个相邻互不相同的字节（压缩无收益）。
func incompressible(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

// dataChunkTypes 解析输出流，返回数据块类型序列（跳过标识块）。
func dataChunkTypes(t *testing.T, stream []byte) []byte {
	t.Helper()
	var types []byte
	for i := 0; i < len(stream); {
		if i+4 > len(stream) {
			t.Fatalf("truncated header at %d", i)
		}
		typ := stream[i]
		n := int(stream[i+1]) | int(stream[i+2])<<8 | int(stream[i+3])<<16
		if i+4+n > len(stream) {
			t.Fatalf("truncated payload at %d", i)
		}
		if typ == chunkCompressed || typ == chunkRaw {
			types = append(types, typ)
		}
		i += 4 + n
	}
	return types
}

// writeChunks 逐块写入并 Flush，返回输出流。
func writeChunks(t *testing.T, F, S int, chunks [][]byte) []byte {
	t.Helper()
	w, err := NewWriter(F, S)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chunks {
		if _, err := w.Write(c); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return w.Bytes()
}

func TestNewWriterParams(t *testing.T) {
	for _, p := range [][2]int{{0, 1}, {1, 0}, {-1, 3}, {3, -2}} {
		if _, err := NewWriter(p[0], p[1]); err != ErrParam {
			t.Errorf("NewWriter(%d,%d) = %v, want ErrParam", p[0], p[1], err)
		}
	}
	if _, err := NewWriter(1, 1); err != nil {
		t.Fatal(err)
	}
}

func TestThresholdEqualityNotCompressed(t *testing.T) {
	// n=32：25 个字面字节 + 7 个相同字节，comp = 26+2 = 28 = 32-32/8。
	// 恰等不算收益，应原样成块。
	data := append(incompressible(25), bytes.Repeat([]byte{200}, 7)...)
	if got := len(compress(data)); got != 28 {
		t.Fatalf("len(comp) = %d, want 28", got)
	}
	stream := writeChunks(t, 1, 1, [][]byte{data})
	types := dataChunkTypes(t, stream)
	if len(types) != 1 || types[0] != chunkRaw {
		t.Errorf("types = % X, want [01]（取等不压缩）", types)
	}
	// 少一个字面字节使 comp=27 < 28，应压缩。
	data2 := append(incompressible(24), bytes.Repeat([]byte{200}, 8)...)
	if got := len(compress(data2)); got != 27 {
		t.Fatalf("len(comp2) = %d, want 27", got)
	}
	types = dataChunkTypes(t, writeChunks(t, 1, 1, [][]byte{data2}))
	if len(types) != 1 || types[0] != chunkCompressed {
		t.Errorf("types = % X, want [00]", types)
	}
}

func TestSmallChunkBoundary(t *testing.T) {
	// 15 字节全 'a'：n<16 不尝试压缩；16 字节全 'a'：8D 61，2 < 14，压缩。
	stream := writeChunks(t, 1, 1, [][]byte{
		bytes.Repeat([]byte{'a'}, 15),
		bytes.Repeat([]byte{'a'}, 16),
	})
	types := dataChunkTypes(t, stream)
	if !bytes.Equal(types, []byte{chunkRaw, chunkCompressed}) {
		t.Fatalf("types = % X, want [01 00]", types)
	}
	// 第二块负载应为 4 字节校验和 + 8D 61。
	off := 10 + 4 + 4 + 15 // 标识块 + 第一块头 + 校验和 + 数据
	payload := stream[off+4+4:]
	if !bytes.Equal(payload, []byte{0x8D, 'a'}) {
		t.Errorf("compressed payload = % X, want [8D 61]", payload)
	}
}

func TestFailureCountResetBySuccess(t *testing.T) {
	// F=3：两次失败后一次成功清零，再两次失败不触发跳过。
	chunks := [][]byte{
		incompressible(100), incompressible(100), // f=2
		bytes.Repeat([]byte{'a'}, 100),           // 成功，f=0
		incompressible(100), incompressible(100), // f=2，不跳过
		bytes.Repeat([]byte{'b'}, 100), // 仍可压缩
	}
	types := dataChunkTypes(t, writeChunks(t, 3, 2, chunks))
	want := []byte{1, 1, 0, 1, 1, 0}
	if !bytes.Equal(types, want) {
		t.Errorf("types = % X, want % X", types, want)
	}
}

func TestSkipAndProbeSequence(t *testing.T) {
	// F=3、S=4：三次失败后 skip=4；随后四块全 'a' 也原样；第八块探测。
	chunks := [][]byte{
		incompressible(100), incompressible(100), incompressible(100), // f=3 → skip=4
		bytes.Repeat([]byte{'a'}, 100), // skip 期间成功也不能压缩 ×4
		bytes.Repeat([]byte{'a'}, 100),
		bytes.Repeat([]byte{'a'}, 100),
		bytes.Repeat([]byte{'a'}, 100),
		bytes.Repeat([]byte{'a'}, 100), // 探测：成功
	}
	types := dataChunkTypes(t, writeChunks(t, 3, 4, chunks))
	want := []byte{1, 1, 1, 1, 1, 1, 1, 0}
	if !bytes.Equal(types, want) {
		t.Errorf("types = % X, want % X", types, want)
	}
}

func TestProbeFailureDoublesAndCaps(t *testing.T) {
	// F=1、S=2：探测失败 skip 翻倍 4、8，封顶 8S=16；探测成功 curS 回到 S。
	raw100 := incompressible(100)
	a100 := bytes.Repeat([]byte{'a'}, 100)
	chunks := [][]byte{raw100}                      // f=1=F → skip=2
	chunks = append(chunks, a100, a100)             // skip 2→0，probe=真
	chunks = append(chunks, raw100)                 // 探测失败 → curS=4, skip=4
	chunks = append(chunks, a100, a100, a100, a100) // skip 4→0
	chunks = append(chunks, raw100)                 // 探测失败 → curS=8, skip=8
	for i := 0; i < 8; i++ {
		chunks = append(chunks, a100)
	}
	chunks = append(chunks, raw100) // 探测失败 → curS=min(16,16)=16, skip=16
	for i := 0; i < 16; i++ {
		chunks = append(chunks, a100)
	}
	chunks = append(chunks, a100)   // 探测成功 → curS=S=2
	chunks = append(chunks, raw100) // f=1=F → skip=curS=2（验证已回到 S）
	chunks = append(chunks, a100, a100)
	chunks = append(chunks, a100) // 探测成功

	types := dataChunkTypes(t, writeChunks(t, 1, 2, chunks))
	want := []byte{1, 1, 1, 1, 1, 1, 1, 1, 1}           // 前 9 块全原样
	want = append(want, bytes.Repeat([]byte{1}, 8)...)  // skip=8
	want = append(want, 1)                              // 探测失败
	want = append(want, bytes.Repeat([]byte{1}, 16)...) // skip=16（封顶）
	want = append(want, 0)                              // 探测成功
	want = append(want, 1, 1, 1)                        // skip=2（curS 已回 S）
	want = append(want, 0)                              // 探测成功
	if !bytes.Equal(types, want) {
		t.Errorf("types =\n% X\nwant\n% X", types, want)
	}
}

func TestSmallChunkConsumesSkipButNotFailures(t *testing.T) {
	// F=1、S=3：一块失败 → skip=3；n<16 的块消耗 skip 但不计失败。
	chunks := [][]byte{
		incompressible(100),            // f=1 → skip=3
		incompressible(10),             // n<16，消耗 skip=2
		bytes.Repeat([]byte{'a'}, 100), // skip=1，成功也原样
		incompressible(5),              // skip=0，probe=真
		incompressible(10),             // probe 期间 n<16 不动 probe
		incompressible(100),            // 探测失败 → curS=min(6,8)=6, skip=6
	}
	for i := 0; i < 6; i++ {
		chunks = append(chunks, bytes.Repeat([]byte{'a'}, 100))
	}
	chunks = append(chunks, bytes.Repeat([]byte{'a'}, 100)) // 探测成功
	types := dataChunkTypes(t, writeChunks(t, 1, 3, chunks))
	want := append(bytes.Repeat([]byte{1}, 12), 0)
	if !bytes.Equal(types, want) {
		t.Errorf("types = % X, want % X", types, want)
	}
}

func TestWriteSplittingIrrelevantFlushMatters(t *testing.T) {
	data := incompressible(1000)
	mk := func(split int, flush bool) []byte {
		w, _ := NewWriter(1, 1)
		w.Write(data[:split])
		w.Write(data[split:])
		if flush {
			w.Flush()
		}
		w.Close()
		return w.Bytes()
	}
	// Write 切分不同、Flush 位置相同 → 输出相同。
	if !bytes.Equal(mk(1, false), mk(999, false)) {
		t.Error("Write 切分改变了输出")
	}
	// Flush 位置不同 → 输出不同。
	w, _ := NewWriter(1, 1)
	w.Write(data[:500])
	w.Flush()
	w.Write(data[500:])
	w.Close()
	if bytes.Equal(w.Bytes(), mk(500, false)) {
		t.Error("Flush 位置不同但输出相同")
	}
	if got := len(dataChunkTypes(t, w.Bytes())); got != 2 {
		t.Errorf("chunks = %d, want 2", got)
	}
}

func TestExact65536Chunk(t *testing.T) {
	w, _ := NewWriter(1, 1)
	if _, err := w.Write(bytes.Repeat([]byte{'a'}, maxUncompressed)); err != nil {
		t.Fatal(err)
	}
	// 恰满 65536 立即成块，无需 Flush。
	if got := len(dataChunkTypes(t, w.Bytes())); got != 1 {
		t.Fatalf("chunks before Close = %d, want 1", got)
	}
	before := len(w.Bytes())
	w.Close()
	if len(w.Bytes()) != before {
		t.Error("Close 多写了字节")
	}
	// 65536+10：一块整 + 一块余量。
	w2, _ := NewWriter(1, 1)
	w2.Write(bytes.Repeat([]byte{'b'}, maxUncompressed+10))
	w2.Close()
	if got := len(dataChunkTypes(t, w2.Bytes())); got != 2 {
		t.Errorf("chunks = %d, want 2", got)
	}
}

func TestEmptyStreamAndZeroWrites(t *testing.T) {
	w, _ := NewWriter(1, 1)
	w.Write(nil)
	w.Write([]byte{})
	w.Flush() // 空缓冲不成块
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(w.Bytes(), identifierChunk) {
		t.Errorf("empty stream = % X, want identifier only", w.Bytes())
	}
}

func TestWriterClosed(t *testing.T) {
	w, _ := NewWriter(1, 1)
	w.Write([]byte("hello"))
	w.Close()
	snap := w.Bytes()
	if _, err := w.Write([]byte("x")); err != ErrClosed {
		t.Errorf("Write after Close = %v", err)
	}
	if err := w.Flush(); err != ErrClosed {
		t.Errorf("Flush after Close = %v", err)
	}
	if err := w.Close(); err != ErrClosed {
		t.Errorf("second Close = %v", err)
	}
	if !bytes.Equal(w.Bytes(), snap) {
		t.Error("被拒绝的写入改变了状态")
	}
}

func TestWriterConcurrency(t *testing.T) {
	w, _ := NewWriter(3, 2)
	var wg sync.WaitGroup
	total := 0
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				w.Write(incompressible(64))
				if i%7 == 0 {
					w.Flush()
				}
				_ = w.Bytes()
			}
		}(g)
	}
	wg.Wait()
	total = 8 * 50 * 64
	w.Close()
	r := NewReader()
	got, err := r.Feed(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if len(got) != total {
		t.Errorf("decoded = %d bytes, want %d", len(got), total)
	}
}

func TestReplayDeterministic(t *testing.T) {
	run := func() []byte {
		w, _ := NewWriter(3, 4)
		for i := 0; i < 20; i++ {
			if i%3 == 0 {
				w.Write(bytes.Repeat([]byte{byte(i)}, 100))
			} else {
				w.Write(incompressible(100))
			}
			if i%4 == 0 {
				w.Flush()
			}
		}
		w.Close()
		return w.Bytes()
	}
	if !bytes.Equal(run(), run()) {
		t.Error("相同操作序列重放得到不同字节")
	}
}
