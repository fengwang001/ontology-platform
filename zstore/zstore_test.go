package zstore

import (
	"bytes"
	"errors"
	"fmt"
	stdadler32 "hash/adler32"
	"sync"
	"testing"
)

// naiveAdler 是测试内的朴素 Adler-32 参照实现。
func naiveAdler(data []byte) uint32 {
	var a, b uint32 = 1, 0
	for _, c := range data {
		a = (a + uint32(c)) % 65521
		b = (b + a) % 65521
	}
	return b<<16 | a
}

// naiveEncode 是测试内的朴素整体编码参照：一次性切分存储块并加头尾。
func naiveEncode(data []byte) []byte {
	out := []byte{0x78, 0x01}
	rest := data
	for len(rest) >= MaxBlockLen {
		out = appendBlock(out, false, rest[:MaxBlockLen])
		rest = rest[MaxBlockLen:]
	}
	out = appendBlock(out, true, rest)
	s := naiveAdler(data)
	return append(out, byte(s>>24), byte(s>>16), byte(s>>8), byte(s))
}

func appendBlock(out []byte, final bool, data []byte) []byte {
	var hdr byte
	if final {
		hdr = 0x01
	}
	n := uint16(len(data))
	out = append(out, hdr, byte(n), byte(n>>8), byte(^n), byte(^n>>8))
	return append(out, data...)
}

// runDecoder 依次喂入各块并 Close，返回交付数据与首个错误。
func runDecoder(chunks ...[]byte) ([]byte, error) {
	d := NewDecoder()
	var firstErr error
	for _, c := range chunks {
		if _, err := d.Write(c); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := d.Close(); firstErr == nil {
		firstErr = err
	}
	return d.Bytes(), firstErr
}

func preview(b []byte) string {
	if len(b) <= 48 {
		return fmt.Sprintf("%x", b)
	}
	return fmt.Sprintf("%x...(%d bytes total)", b[:48], len(b))
}

func encodeAll(t *testing.T, data []byte) []byte {
	t.Helper()
	e := NewEncoder()
	if _, err := e.Write(data); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return e.Bytes()
}

func TestAdler32Wikipedia(t *testing.T) {
	s := newAdler32()
	s.Write([]byte("Wikipedia"))
	got := s.Sum32()
	want := uint32(0x11E60398)
	t.Logf("输入=%q 输出=0x%08X 判定依据=Wikipedia 已知值 0x%08X", "Wikipedia", got, want)
	if got != want {
		t.Fatalf("got 0x%08X, want 0x%08X", got, want)
	}
	if std := stdadler32.Checksum([]byte("Wikipedia")); got != std {
		t.Fatalf("mismatch with stdlib: got 0x%08X, std 0x%08X", got, std)
	}
}

func TestAdler32AllFF(t *testing.T) {
	data := bytes.Repeat([]byte{0xFF}, 200000)
	s := newAdler32()
	s.Write(data[:7])
	s.Write(data[7:65536])
	s.Write(data[65536:])
	got := s.Sum32()
	want := naiveAdler(data)
	std := stdadler32.Checksum(data)
	t.Logf("输入=200000 个 0xFF 输出=0x%08X 判定依据=朴素逐字节取模 0x%08X 且与标准库 0x%08X 一致", got, want, std)
	if got != want || got != std {
		t.Fatalf("got 0x%08X, naive 0x%08X, std 0x%08X", got, want, std)
	}
}

func TestEmptyInput(t *testing.T) {
	e := NewEncoder()
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := e.Bytes()
	want := []byte{0x78, 0x01, 0x01, 0x00, 0x00, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x01}
	t.Logf("输入=空 输出=%x 判定依据=规范给定空输入字节序列 %x", got, want)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
	if !bytes.Equal(got, naiveEncode(nil)) {
		t.Fatalf("mismatch with naive encode: %x", naiveEncode(nil))
	}
}

func TestBlockSplitBoundary(t *testing.T) {
	for _, size := range []int{1, MaxBlockLen - 1, MaxBlockLen, MaxBlockLen + 1, 2*MaxBlockLen + 3} {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(i * 31)
		}
		got := encodeAll(t, data)
		want := naiveEncode(data)
		t.Logf("输入长度=%d 输出长度=%d 判定依据=与朴素整体编码逐字节一致", size, len(got))
		if !bytes.Equal(got, want) {
			t.Fatalf("size %d: output mismatch with naive encode", size)
		}
		if size == MaxBlockLen {
			// 恰好 65535：一个非终满块 + 0 字节终块。
			if got[2] != 0x00 {
				t.Fatalf("first block should be non-final, hdr=%x", got[2])
			}
			tail := got[2+5+MaxBlockLen:]
			if !bytes.Equal(tail[:5], []byte{0x01, 0x00, 0x00, 0xFF, 0xFF}) {
				t.Fatalf("final block should be empty, got %x", tail[:5])
			}
			t.Logf("65535 边界: 首块头=%02x 终块=%x 判定依据=满块非终+空终块", got[2], tail[:5])
		}
		if size == MaxBlockLen+1 {
			// 65536：一个非终满块 + 1 字节终块。
			tail := got[2+5+MaxBlockLen:]
			if tail[0] != 0x01 || tail[1] != 0x01 || tail[2] != 0x00 || tail[3] != 0xFE || tail[4] != 0xFF {
				t.Fatalf("final block should carry 1 byte, got %x", tail[:5])
			}
			t.Logf("65536 边界: 终块头 5 字节=%x 判定依据=终块 LEN=1 NLEN=FFFE", tail[:5])
		}
	}
}

func TestEncoderWriteSplitIndependent(t *testing.T) {
	data := make([]byte, 3*MaxBlockLen+12345)
	for i := range data {
		data[i] = byte(i*7 + i>>8)
	}
	want := naiveEncode(data)

	splits := map[string][][]byte{
		"整段一次写入": {data},
		"逐字节写入":  nil,
		"不均匀三段":  {data[:1], data[1 : MaxBlockLen+1], data[MaxBlockLen+1:]},
		"块边界对齐":  {data[:MaxBlockLen], data[MaxBlockLen : 2*MaxBlockLen], data[2*MaxBlockLen:]},
	}
	for name, chunks := range splits {
		e := NewEncoder()
		if name == "逐字节写入" {
			for i := 0; i < len(data); i++ {
				if _, err := e.Write(data[i : i+1]); err != nil {
					t.Fatalf("%s: Write: %v", name, err)
				}
			}
		} else {
			for _, c := range chunks {
				if _, err := e.Write(c); err != nil {
					t.Fatalf("%s: Write: %v", name, err)
				}
			}
		}
		if err := e.Close(); err != nil {
			t.Fatalf("%s: Close: %v", name, err)
		}
		got := e.Bytes()
		t.Logf("切分方式=%s 输出长度=%d 判定依据=与朴素整体编码逐字节一致", name, len(got))
		if !bytes.Equal(got, want) {
			t.Fatalf("%s: output differs from naive encode", name)
		}
	}
}

func TestEncoderDeterministicReplay(t *testing.T) {
	data := []byte("deterministic replay check 相同输入重放")
	a, b := encodeAll(t, data), encodeAll(t, data)
	t.Logf("输入=%q 输出=%x 判定依据=两次编码逐字节相同", data, a)
	if !bytes.Equal(a, b) {
		t.Fatalf("replay mismatch: %x vs %x", a, b)
	}
}

func TestEncoderCloseRejectsFurtherCalls(t *testing.T) {
	e := NewEncoder()
	if _, err := e.Write([]byte("abc")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	snap := e.Bytes()
	if _, err := e.Write([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Write after Close: got %v, want ErrClosed", err)
	}
	if err := e.Close(); !errors.Is(err, ErrClosed) {
		t.Fatalf("second Close: got %v, want ErrClosed", err)
	}
	t.Logf("输入=Close 后 Write/Close 输出=%v/%v 判定依据=均返回 ErrClosed 且输出不变", ErrClosed, ErrClosed)
	if !bytes.Equal(e.Bytes(), snap) {
		t.Fatalf("rejected calls changed output")
	}
}

func TestDecodeEmptyStream(t *testing.T) {
	stream := []byte{0x78, 0x01, 0x01, 0x00, 0x00, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x01}
	got, err := runDecoder(stream)
	t.Logf("输入=%x 交付=%x 错误=%v 判定依据=空流交付 0 字节且无错误", stream, got, err)
	if err != nil || len(got) != 0 {
		t.Fatalf("got delivered=%x err=%v", got, err)
	}
}

func TestHeaderErrorOrder(t *testing.T) {
	cases := []struct {
		name string
		hdr  [2]byte
		want error
	}{
		{"方法错+窗口错+校验错+字典位", [2]byte{0x99, 0xFF}, ErrMethod},
		{"方法对+窗口错+校验错+字典位", [2]byte{0x88, 0xFF}, ErrWindow},
		{"方法窗口对+校验错+字典位", [2]byte{0x78, 0xFF}, ErrHeaderCheck},
		{"校验对+字典位", [2]byte{0x78, 0x20}, ErrDict},
		{"仅方法错", [2]byte{0x77, 0x01}, ErrMethod},
		{"仅窗口错", [2]byte{0x88, 0x04}, ErrWindow},
	}
	for _, c := range cases {
		d := NewDecoder()
		_, err := d.Write(c.hdr[:])
		t.Logf("输入头=%x 错误=%v 判定依据=头部按序只报第一个错误(%s)", c.hdr, err, c.name)
		if !errors.Is(err, c.want) {
			t.Fatalf("hdr %x: got %v, want %v", c.hdr, err, c.want)
		}
	}
	d := NewDecoder()
	if _, err := d.Write([]byte{0x78, 0x01}); err != nil {
		t.Fatalf("valid header rejected: %v", err)
	}
}

func TestBlockHeaderErrors(t *testing.T) {
	cases := []struct {
		name string
		hdr  byte
		want error
	}{
		{"高5位非零", 0x08, ErrBlockHeaderBits},
		{"高5位全置", 0xF9, ErrBlockHeaderBits},
		{"类型01不支持", 0x02, ErrBlockTypeUnsupported},
		{"类型10不支持", 0x04, ErrBlockTypeUnsupported},
		{"类型11保留", 0x06, ErrBlockTypeReserved},
	}
	for _, c := range cases {
		stream := append([]byte{0x78, 0x01}, c.hdr)
		d := NewDecoder()
		_, err := d.Write(stream)
		t.Logf("输入=%x 错误=%v 判定依据=块头校验(%s)", stream, err, c.name)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

func TestNLENError(t *testing.T) {
	stream := []byte{0x78, 0x01, 0x01, 0x05, 0x00, 0x00, 0x00}
	d := NewDecoder()
	_, err := d.Write(stream)
	t.Logf("输入=%x 错误=%v 判定依据=NLEN=0x0000 不是 LEN=0x0005 的补码", stream, err)
	if !errors.Is(err, ErrNLEN) {
		t.Fatalf("got %v, want ErrNLEN", err)
	}
}

func TestZeroLenNonFinalBlock(t *testing.T) {
	// 非终块 LEN=0 合法：空非终块 + 终块 "hi"。
	stream := []byte{0x78, 0x01, 0x00, 0x00, 0x00, 0xFF, 0xFF}
	stream = append(stream, naiveEncode([]byte("hi"))[2:]...)
	got, err := runDecoder(stream)
	t.Logf("输入=%x 交付=%q 错误=%v 判定依据=非终块 LEN 可为 0", stream, got, err)
	if err != nil || string(got) != "hi" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestChecksumError(t *testing.T) {
	data := []byte("checksum me")
	stream := encodeAll(t, data)
	stream[len(stream)-1] ^= 0xFF
	got, err := runDecoder(stream)
	t.Logf("输入=%x(末字节翻转) 交付=%q 错误=%v 判定依据=尾部与已解数据 Adler-32 不符", stream, got, err)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("got %v, want ErrChecksum", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("delivered %q, want %q", got, data)
	}
}

func TestTrailingDataError(t *testing.T) {
	stream := append(encodeAll(t, []byte("x")), 0x00)
	_, err := runDecoder(stream)
	t.Logf("输入=%x 错误=%v 判定依据=尾部之后多出 1 字节", stream, err)
	if !errors.Is(err, ErrTrailingData) {
		t.Fatalf("got %v, want ErrTrailingData", err)
	}
}

func TestTruncatedStream(t *testing.T) {
	stream := encodeAll(t, []byte("truncate me please"))
	for _, k := range []int{0, 1, 2, 6, len(stream) - 4, len(stream) - 1} {
		d := NewDecoder()
		if _, err := d.Write(stream[:k]); err != nil {
			t.Fatalf("prefix %d: Write: %v", k, err)
		}
		err := d.Close()
		t.Logf("输入=流前 %d/%d 字节 错误=%v 判定依据=未读完尾部即 Close 报截断", k, len(stream), err)
		if !errors.Is(err, ErrTruncated) {
			t.Fatalf("prefix %d: got %v, want ErrTruncated", k, err)
		}
		if _, err := d.Write([]byte{0x00}); !errors.Is(err, ErrPoisoned) {
			t.Fatalf("prefix %d: Write after truncation got %v, want ErrPoisoned", k, err)
		}
		if err := d.Close(); !errors.Is(err, ErrPoisoned) {
			t.Fatalf("prefix %d: Close after truncation got %v, want ErrPoisoned", k, err)
		}
	}
}

func TestPoisonedStateSticky(t *testing.T) {
	d := NewDecoder()
	if _, err := d.Write([]byte{0x78, 0x01, 0x01, 0x05, 0x00, 0x00, 0x00}); !errors.Is(err, ErrNLEN) {
		t.Fatalf("setup: got %v, want ErrNLEN", err)
	}
	before := d.Delivered()
	if _, err := d.Write([]byte{0x78, 0x01}); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("Write: got %v, want ErrPoisoned", err)
	}
	if err := d.Close(); !errors.Is(err, ErrPoisoned) {
		t.Fatalf("Close: got %v, want ErrPoisoned", err)
	}
	t.Logf("输入=出错后继续 Write/Close 错误=%v 判定依据=粘滞失败态且已交付数不变(%d)", ErrPoisoned, before)
	if d.Delivered() != before {
		t.Fatalf("poisoned calls changed delivered count: %d -> %d", before, d.Delivered())
	}
}

func TestIncrementalDelivery(t *testing.T) {
	data := bytes.Repeat([]byte("ab"), 50) // 100 字节
	stream := encodeAll(t, data)
	d := NewDecoder()
	// 头 2 字节 + 块头 5 字节 + 40 数据字节。
	if _, err := d.Write(stream[:47]); err != nil {
		t.Fatalf("Write: %v", err)
	}
	t.Logf("输入=流前 47 字节 已交付=%d 判定依据=头2+块头5+数据40 边收边交付", d.Delivered())
	if d.Delivered() != 40 {
		t.Fatalf("Delivered=%d, want 40", d.Delivered())
	}
	if _, err := d.Write(stream[47:]); err != nil {
		t.Fatalf("Write rest: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !bytes.Equal(d.Bytes(), data) || d.Delivered() != len(data) {
		t.Fatalf("final delivered mismatch")
	}
}

func TestAllSplitPointsConsistent(t *testing.T) {
	data := make([]byte, 1000)
	for i := range data {
		data[i] = byte(i*13 + 5)
	}
	stream := encodeAll(t, data)
	t.Logf("输入=数据 %d 字节, 流 %d 字节 判定依据=所有 %d 个切分点的两段喂入结果与整体喂入一致", len(data), len(stream), len(stream)+1)

	wantData, wantErr := runDecoder(stream)
	if wantErr != nil {
		t.Fatalf("whole feed: %v", wantErr)
	}
	for i := 0; i <= len(stream); i++ {
		got, err := runDecoder(stream[:i], stream[i:])
		if err != nil || !bytes.Equal(got, wantData) {
			t.Fatalf("split at %d: err=%v delivered equal=%v", i, err, bytes.Equal(got, wantData))
		}
	}
	// 逐字节喂入。
	d := NewDecoder()
	for i := 0; i < len(stream); i++ {
		if _, err := d.Write(stream[i : i+1]); err != nil {
			t.Fatalf("byte %d: %v", i, err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatalf("byte-by-byte Close: %v", err)
	}
	if !bytes.Equal(d.Bytes(), wantData) {
		t.Fatalf("byte-by-byte delivered mismatch")
	}
	t.Logf("输出=交付 %d 字节, 全部切分一致 判定通过", len(wantData))
}

func TestAllSplitPointsConsistentAcrossBlockBoundary(t *testing.T) {
	data := make([]byte, MaxBlockLen+1)
	for i := range data {
		data[i] = byte(i >> 4)
	}
	stream := encodeAll(t, data)
	t.Logf("输入=数据 %d 字节(跨块边界), 流 %d 字节 判定依据=所有切分点结果一致", len(data), len(stream))
	wantData, wantErr := runDecoder(stream)
	if wantErr != nil {
		t.Fatalf("whole feed: %v", wantErr)
	}
	for i := 0; i <= len(stream); i++ {
		got, err := runDecoder(stream[:i], stream[i:])
		if err != nil || !bytes.Equal(got, wantData) {
			t.Fatalf("split at %d: err=%v delivered equal=%v", i, err, bytes.Equal(got, wantData))
		}
	}
	t.Logf("输出=交付 %d 字节, %d 个切分点全部一致 判定通过", len(wantData), len(stream)+1)
}

func TestAllSplitPointsErrorConsistent(t *testing.T) {
	data := []byte("error stream split consistency")
	stream := encodeAll(t, data)
	stream[len(stream)-2] ^= 0x01 // 破坏 Adler-32 尾部
	t.Logf("输入=尾部位翻转的流 %d 字节 判定依据=所有切分点都报 ErrChecksum 且交付相同", len(stream))
	wantData, wantErr := runDecoder(stream)
	if !errors.Is(wantErr, ErrChecksum) {
		t.Fatalf("whole feed: got %v, want ErrChecksum", wantErr)
	}
	for i := 0; i <= len(stream); i++ {
		got, err := runDecoder(stream[:i], stream[i:])
		if !errors.Is(err, ErrChecksum) || !bytes.Equal(got, wantData) {
			t.Fatalf("split at %d: err=%v delivered equal=%v", i, err, bytes.Equal(got, wantData))
		}
	}
	t.Logf("输出=全部切分点报 %v 且交付 %d 字节一致 判定通过", wantErr, len(wantData))
}

func TestRoundTripBoundarySizes(t *testing.T) {
	for _, size := range []int{0, 1, MaxBlockLen, MaxBlockLen + 1, 4*MaxBlockLen + 7} {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(i*3 + 1)
		}
		stream := encodeAll(t, data)
		got, err := runDecoder(stream)
		t.Logf("输入长度=%d 流长度=%d 交付=%d 错误=%v 判定依据=编解码往返一致", size, len(stream), len(got), err)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("size %d: err=%v match=%v", size, err, bytes.Equal(got, data))
		}
	}
}

func TestConcurrentEncoder(t *testing.T) {
	const goroutines, writes, payload = 8, 50, 2000
	e := NewEncoder()
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			buf := bytes.Repeat([]byte{byte(g + 1)}, payload)
			for i := 0; i < writes; i++ {
				if _, err := e.Write(buf); err != nil {
					t.Errorf("Write: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	stream := e.Bytes()
	got, err := runDecoder(stream)
	if err != nil {
		t.Fatalf("decode concurrent output: %v", err)
	}
	// 等价于某个串行顺序：总长度一致且每种字节计数一致。
	if len(got) != goroutines*writes*payload {
		t.Fatalf("delivered %d, want %d", len(got), goroutines*writes*payload)
	}
	counts := map[byte]int{}
	for _, b := range got {
		counts[b]++
	}
	for g := 0; g < goroutines; g++ {
		if counts[byte(g+1)] != writes*payload {
			t.Fatalf("byte %d count %d, want %d", g+1, counts[byte(g+1)], writes*payload)
		}
	}
	t.Logf("输入=%d goroutine 并发写 输出=流 %d 字节 判定依据=可解码且各字节计数符合某串行顺序", goroutines, len(stream))
}

func TestConcurrentDecoder(t *testing.T) {
	data := make([]byte, 4096)
	for i := range data {
		data[i] = byte(i)
	}
	stream := encodeAll(t, data)
	// 顺序喂入作对照。
	wantData, wantErr := runDecoder(stream)
	if wantErr != nil || !bytes.Equal(wantData, data) {
		t.Fatalf("sequential feed failed: %v", wantErr)
	}
	// 并发喂入：互斥保证等价于某个串行顺序，此处验证无数据竞争、
	// 且每次 Write 要么完整消费要么返回已定义错误。
	const chunks = 16
	d := NewDecoder()
	var wg sync.WaitGroup
	for c := 0; c < chunks; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			lo := c * len(stream) / chunks
			hi := (c + 1) * len(stream) / chunks
			n, err := d.Write(stream[lo:hi])
			if err == nil && n != hi-lo {
				t.Errorf("consumed %d of %d without error", n, hi-lo)
			}
		}(c)
	}
	wg.Wait()
	_ = d.Close()
	if d.Delivered() > len(stream) {
		t.Fatalf("delivered %d exceeds stream size %d", d.Delivered(), len(stream))
	}
	t.Logf("输入=%d 块并发喂入 已交付=%d 判定依据=无竞态且交付数不超过流长(乱序交织可能触发校验错)", chunks, d.Delivered())
}
