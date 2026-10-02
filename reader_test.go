package ontology

import (
	"bytes"
	"errors"
	"testing"
)

func feedAll(t *testing.T, r *Reader, raw []byte) []byte {
	t.Helper()
	out, err := r.Feed(raw)
	if err != nil {
		t.Fatalf("unexpected feed error: %v", err)
	}
	return out
}

func TestReaderRoundTrip(t *testing.T) {
	parts := [][]byte{
		[]byte("hello world"),
		bytes.Repeat([]byte{'a'}, 100),
		bytes.Repeat([]byte{0x55}, 65536),
		incompressibleBlock(300, 7),
		{},
		[]byte("tail"),
	}
	var input bytes.Buffer
	w, _ := NewWriter(3, 4)
	for _, p := range parts {
		if _, err := w.Write(p); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		input.Write(p)
	}
	mustClose(t, w)
	raw := w.Bytes()

	// 一次性 Feed
	r := NewReader()
	got := feedAll(t, r, raw)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, input.Bytes()) {
		t.Fatalf("one-shot mismatch: got %d bytes want %d", len(got), input.Len())
	}
}

func TestReaderRepeatedIdentifierAndSkippable(t *testing.T) {
	// 手工构造：标识 + first + 标识（流中重复）+ 可跳过块 + second
	skipPayload := []byte("ignored payload!!")
	var built bytes.Buffer
	built.Write(identifierChunk)
	w2, _ := NewWriter(2, 2)
	flushBlock(t, w2, []byte("first"))
	mustClose(t, w2)
	firstChunk := w2.Bytes()[len(identifierChunk):]
	built.Write(firstChunk)
	built.Write(identifierChunk)
	built.Write([]byte{0x80, byte(len(skipPayload)), 0, 0})
	built.Write(skipPayload)
	w3, _ := NewWriter(2, 2)
	flushBlock(t, w3, []byte("second chunk data"))
	mustClose(t, w3)
	built.Write(w3.Bytes()[len(identifierChunk):])

	r := NewReader()
	got := feedAll(t, r, built.Bytes())
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	want := []byte("firstsecond chunk data")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestReaderMissingIdentifier(t *testing.T) {
	// 首块不是标识：以数据块开头
	r := NewReader()
	out, err := r.Feed([]byte{0x00, 0x06, 0x00, 0x00, 0, 0, 0, 0, 0x8D, 0x61})
	if !errors.Is(err, ErrNoIdentifier) {
		t.Fatalf("err=%v", err)
	}
	var fe *FrameError
	if !errors.As(err, &fe) || fe.Offset != 0 {
		t.Fatalf("offset=%d", fe.Offset)
	}
	if len(out) != 0 {
		t.Fatalf("output before error must be empty: %d", len(out))
	}
	// 粘滞
	out2, err2 := r.Feed([]byte{0xFF})
	if !errors.Is(err2, ErrNoIdentifier) || len(out2) != 0 || err2 != err {
		t.Fatalf("sticky: err=%v out=%d same=%v", err2, len(out2), err2 == err)
	}
	if err := r.Close(); !errors.Is(err, ErrNoIdentifier) {
		t.Fatalf("close after error: %v", err)
	}

	// 空流 Close
	r2 := NewReader()
	if err := r2.Close(); !errors.Is(err, ErrNoIdentifier) {
		t.Fatalf("empty close: %v", err)
	}
}

func TestReaderBadIdentifier(t *testing.T) {
	// 长度不是 6
	r := NewReader()
	_, err := r.Feed([]byte{0xFF, 0x05, 0, 0, 's', 'N', 'a', 'P', 'p'})
	if !errors.Is(err, ErrBadIdentifier) {
		t.Fatalf("len 5: %v", err)
	}
	if fe := err.(*FrameError); fe.Offset != 0 {
		t.Fatalf("offset=%d", fe.Offset)
	}
	// 长度 6 但内容错误（分两次 Feed，错误须在负载收齐后）
	r2 := NewReader()
	if _, err := r2.Feed([]byte{0xFF, 0x06, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := r2.Feed([]byte("sNaPpX")); !errors.Is(err, ErrBadIdentifier) {
		t.Fatalf("bad magic: %v", err)
	}
}

func TestReaderReserved(t *testing.T) {
	for _, typ := range []byte{0x02, 0x10, 0x7F} {
		r := NewReader()
		feedAll(t, r, identifierChunk)
		_, err := r.Feed([]byte{typ, 0x10, 0, 0})
		if !errors.Is(err, ErrReserved) {
			t.Fatalf("type %#x: %v", typ, err)
		}
		if fe := err.(*FrameError); fe.Offset != 10 {
			t.Fatalf("type %#x offset=%d", typ, fe.Offset)
		}
	}
}

func TestReaderChunkLen(t *testing.T) {
	r := NewReader()
	feedAll(t, r, identifierChunk)
	// 数据块长度 3，块头收齐即判，不等负载
	_, err := r.Feed([]byte{0x00, 0x03, 0, 0, 1, 2, 3})
	if !errors.Is(err, ErrChunkLen) {
		t.Fatalf("len 3: %v", err)
	}

	r2 := NewReader()
	feedAll(t, r2, identifierChunk)
	// 长度 65541
	_, err = r2.Feed([]byte{0x01, 0x05, 0x00, 0x01})
	if !errors.Is(err, ErrChunkLen) {
		t.Fatalf("len 65541: %v", err)
	}
}

func TestReaderDecodeAndChecksum(t *testing.T) {
	w, _ := NewWriter(1, 1)
	flushBlock(t, w, bytes.Repeat([]byte{'a'}, 100))
	raw := w.Bytes()
	mustClose(t, w)

	// 找到 RLE 数据块，截断其负载：构造一个截断的重复令牌负载
	r := NewReader()
	feedAll(t, r, identifierChunk)
	// 0x00 类型，负载：4 字节校验和 + 控制字节 0x80（重复 3 次）但缺重复字节
	bad := []byte{chunkRLE, 0x05, 0, 0, 1, 2, 3, 4, 0x80}
	_, err := r.Feed(bad)
	if !errors.Is(err, ErrDecode) {
		t.Fatalf("truncated run token: %v", err)
	}

	// 校验和错误：取合法流翻转校验和一位
	r2 := NewReader()
	feedAll(t, r2, identifierChunk)
	corrupt := append([]byte(nil), raw[len(identifierChunk):]...)
	corrupt[4] ^= 0x01
	_, err = r2.Feed(corrupt)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("checksum: %v", err)
	}

	// 解码超限：构造解出 >65536 的 RLE 负载。单块负载最多 65540，
	// 用 0xFF（130 次重复）令牌填满并超出：505 个令牌 -> 65650 字节。
	var over bytes.Buffer
	over.Write([]byte{chunkRLE, 0, 0, 0, 0, 0, 0, 0}) // 占位头，稍后修
	tokens := (maxChunkData+129)/130 + 1
	for i := 0; i < tokens; i++ {
		over.Write([]byte{0xFF, 'x'})
	}
	payloadLen := over.Len() - 4
	over.Bytes()[1] = byte(payloadLen)
	over.Bytes()[2] = byte(payloadLen >> 8)
	over.Bytes()[3] = byte(payloadLen >> 16)
	r3 := NewReader()
	feedAll(t, r3, identifierChunk)
	_, err = r3.Feed(over.Bytes())
	if !errors.Is(err, ErrDecode) {
		t.Fatalf("over-limit decode: %v", err)
	}
}

func TestReaderTruncated(t *testing.T) {
	w, _ := NewWriter(1, 1)
	flushBlock(t, w, bytes.Repeat([]byte{'a'}, 100))
	mustClose(t, w)
	full := w.Bytes()

	// 每个截断点（0..len-1）：块边界 Close 成功，其余 ErrTruncated
	for cut := 0; cut < len(full); cut++ {
		r := NewReader()
		out, err := r.Feed(full[:cut])
		if err != nil {
			t.Fatalf("cut=%d feed err %v", cut, err)
		}
		cerr := r.Close()
		atBoundary := cut == 0 || cut == len(identifierChunk) || cut == len(full)
		if atBoundary && cut > 0 {
			if cerr != nil {
				t.Fatalf("cut=%d boundary should be complete: %v", cut, cerr)
			}
			continue
		}
		if cut == 0 {
			if !errors.Is(cerr, ErrNoIdentifier) {
				t.Fatalf("cut=0: %v", cerr)
			}
			continue
		}
		if !errors.Is(cerr, ErrTruncated) {
			t.Fatalf("cut=%d: %v out=%d", cut, cerr, len(out))
		}
	}
}

func TestReaderErrorOrder(t *testing.T) {
	// 标识块收到一半 Close：ErrTruncated 优先于 ErrNoIdentifier
	r := NewReader()
	if _, err := r.Feed([]byte{0xFF, 0x06}); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("partial identifier close: %v", err)
	}

	// 数据块负载收到一半 Close：ErrTruncated，偏移为块头位置
	r2 := NewReader()
	feedAll(t, r2, identifierChunk)
	w, _ := NewWriter(1, 1)
	flushBlock(t, w, []byte("payload bytes here"))
	mustClose(t, w)
	chunk := w.Bytes()[len(identifierChunk):]
	if _, err := r2.Feed(chunk[:5]); err != nil {
		t.Fatal(err)
	}
	err := r2.Close()
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("partial payload: %v", err)
	}
	if fe := err.(*FrameError); fe.Offset != 10 {
		t.Fatalf("offset=%d", fe.Offset)
	}

	// Feed 中出错块之前已解出的数据必须返回
	w4, _ := NewWriter(1, 1)
	flushBlock(t, w4, []byte("good block one!!!"))
	flushBlock(t, w4, []byte("good block two!!!"))
	mustClose(t, w4)
	good := w4.Bytes()
	r4 := NewReader()
	head := append([]byte(nil), good...)
	head = append(head, 0x02, 0x00, 0x00, 0x00) // 保留块
	out, err := r4.Feed(head)
	if !errors.Is(err, ErrReserved) {
		t.Fatalf("err=%v", err)
	}
	if string(out) != "good block one!!!good block two!!!" {
		t.Fatalf("prior output lost: %q", out)
	}
}

func TestReaderMaxBuffered(t *testing.T) {
	w, _ := NewWriter(1, 1)
	flushBlock(t, w, make([]byte, 65536)) // 原样 65536（填充零是可压缩的，改为不可压）
	mustClose(t, w)
	_ = w

	w2, _ := NewWriter(1, 1)
	big := incompressibleBlock(65536, 3)
	flushBlock(t, w2, big)
	mustClose(t, w2)
	r := NewReader()
	feedAll(t, r, w2.Bytes())
	if r.maxBuffered > 65544 {
		t.Fatalf("maxBuffered=%d exceeds 65544", r.maxBuffered)
	}
	if r.maxBuffered != 65544 {
		t.Fatalf("maxBuffered=%d want 65544", r.maxBuffered)
	}

	// 可跳过块不缓冲负载：构造一个 60000 字节的可跳过块
	r2 := NewReader()
	feedAll(t, r2, identifierChunk)
	hdr := []byte{0x80, 0x60, 0xEA, 0} // 60000
	feed := append([]byte(nil), hdr...)
	feed = append(feed, make([]byte, 60000)...)
	out, err := r2.Feed(feed)
	if err != nil || len(out) != 0 {
		t.Fatalf("skippable: err=%v out=%d", err, len(out))
	}
	if r2.maxBuffered > 65544 {
		t.Fatalf("skippable buffered: %d", r2.maxBuffered)
	}
	if err := r2.Close(); err != nil {
		t.Fatal(err)
	}
}
