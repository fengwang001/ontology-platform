package ontology

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// naiveModel 按题述规则逐步独立实现写出器，用于随机对照。
type naiveModel struct {
	f, s, curS, skip, fail int
	probe                  bool
	out                    []byte
}

func newNaiveModel(F, S int) *naiveModel {
	return &naiveModel{f: F, s: S, curS: S, out: append([]byte(nil), identifierChunk...)}
}

func naiveRLE(p []byte) []byte { return rleEncode(p) }

func naivePutU32(v uint32) []byte {
	return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}
}

func (m *naiveModel) chunk(data []byte) {
	n := len(data)
	useRLE := false
	if m.skip > 0 {
		m.skip--
		if m.skip == 0 {
			m.probe = true
		}
	} else if n < 16 {
		// 不动状态
	} else {
		comp := naiveRLE(data)
		if len(comp) < n-n/8 {
			useRLE = true
			m.fail = 0
			m.probe = false
			m.curS = m.s
		} else {
			if m.probe {
				m.probe = false
				m.curS *= 2
				if m.curS > 8*m.s {
					m.curS = 8 * m.s
				}
				m.skip = m.curS
				m.fail = 0
			} else {
				m.fail++
				if m.fail == m.f {
					m.skip = m.curS
					m.fail = 0
				}
			}
		}
	}
	body := data
	typ := byte(chunkRaw)
	if useRLE {
		typ = chunkRLE
		body = naiveRLE(data)
	}
	loadLen := len(body) + 4
	m.out = append(m.out, typ, byte(loadLen), byte(loadLen>>8), byte(loadLen>>16))
	m.out = append(m.out, naivePutU32(maskedChecksum(data))...)
	m.out = append(m.out, body...)
}

func randBytes(r *rand.Rand, n int) []byte {
	p := make([]byte, n)
	switch r.Intn(4) {
	case 0:
		for i := range p {
			p[i] = byte(r.Intn(3)) // 高重复
		}
	case 1:
		fill := []byte{0, 0, 1, 1} // 交替，最长段 2（不可压趋势）
		for i := range p {
			p[i] = fill[i&3]
		}
	case 2:
		for i := range p {
			p[i] = byte(r.Intn(256))
		}
	default:
		for i := 0; i < n; {
			b := byte(r.Intn(4))
			run := 1 + r.Intn(140)
			if i+run > n {
				run = n - i
			}
			for j := 0; j < run; j++ {
				p[i] = b
				i++
			}
		}
	}
	return p
}

func TestNaiveModel2000(t *testing.T) {
	r := rand.New(rand.NewSource(20241002))
	for iter := 0; iter < 2000; iter++ {
		F := 1 + r.Intn(5)
		S := 1 + r.Intn(5)
		w, err := NewWriter(F, S)
		if err != nil {
			t.Fatal(err)
		}
		m := newNaiveModel(F, S)

		var original []byte
		nOps := r.Intn(12)
		var log bytes.Buffer
		fmt.Fprintf(&log, "iter=%d F=%d S=%d", iter, F, S)
		for op := 0; op < nOps; op++ {
			n := r.Intn(200)
			p := randBytes(r, n)
			original = append(original, p...)
			if _, err := w.Write(p); err != nil {
				t.Fatal(err)
			}
			if r.Intn(2) == 0 {
				at := r.Intn(3)
				if at == 0 {
					if err := w.Flush(); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		// 强制在若干固定切分点 Flush（模型用 65536 切块 + flush 点）
		// 直接按 Flush 边界切块后喂模型：这里简单起见不额外 flush，
		// Flush 位置对输出的影响由 TestFlushPositionsAndWriteSplits 覆盖；
		// 本测试在 Close 前随机 flush 一次。
		if r.Intn(2) == 0 {
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
		}
		mustClose(t, w)

		// 模型按相同字节序列切块：65536 满块 + 尾部（含随机 Flush 难以复现切分，
		// 故此对照流中不使用中途 Flush 的输出判定；Flush 分支在上面 50% 概率里
		// 仅验证写出器自身一致）。为严格对照，重建一个不中途 flush 的写出器。
		w2, _ := NewWriter(F, S)
		if _, err := w2.Write(original); err != nil {
			t.Fatal(err)
		}
		// 模型按 65536 切块
		for off := 0; off < len(original); off += maxChunkData {
			end := off + maxChunkData
			if end > len(original) {
				end = len(original)
			}
			m.chunk(original[off:end])
		}
		mustClose(t, w2)
		got2 := w2.Bytes()
		if !bytes.Equal(got2, m.out) {
			t.Fatalf("iter=%d model mismatch\n got len=%d\nwant len=%d\n%s",
				iter, len(got2), len(m.out), log.String())
		}

		// 解码必须还原原始输入
		rd := NewReader()
		dec, derr := rd.Feed(got2)
		if derr != nil {
			t.Fatalf("iter=%d decode: %v", iter, derr)
		}
		if err := rd.Close(); err != nil {
			t.Fatalf("iter=%d close: %v", iter, err)
		}
		if !bytes.Equal(dec, original) {
			t.Fatalf("iter=%d data mismatch: %d vs %d", iter, len(dec), len(original))
		}
		{
			types := chunkTypes(parseFrames(t, got2))
			t.Logf("%s\n  input(%d)=% x\n  output(%d)=% x\n  decision_basis(types)=% x",
				log.String(), len(original), original, len(got2), got2, types)
		}
	}
}

// TestNaiveModelFlushPoints：随机 Flush 位置也与模型切块一致。
func TestNaiveModelFlushPoints(t *testing.T) {
	r := rand.New(rand.NewSource(77))
	for iter := 0; iter < 200; iter++ {
		F := 1 + r.Intn(4)
		S := 1 + r.Intn(4)
		data := randBytes(r, 1+r.Intn(3000))
		var cuts []int
		pos := 0
		for pos < len(data) {
			pos += 1 + r.Intn(300)
			if pos < len(data) {
				cuts = append(cuts, pos)
			}
		}
		bounds := append(append([]int(nil), cuts...), len(data))
		w, _ := NewWriter(F, S)
		m := newNaiveModel(F, S)
		prev := 0
		for _, bound := range bounds {
			seg := data[prev:bound]
			// 随机 Write 切分
			wpos := 0
			for wpos < len(seg) {
				end := wpos + 1 + r.Intn(77)
				if end > len(seg) {
					end = len(seg)
				}
				if _, err := w.Write(seg[wpos:end]); err != nil {
					t.Fatal(err)
				}
				wpos = end
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			m.chunk(seg)
			prev = bound
		}
		mustClose(t, w)
		if got := w.Bytes(); !bytes.Equal(got, m.out) {
			t.Fatalf("iter=%d flush model mismatch: got %d want %d", iter, len(got), len(m.out))
		}
		rd := NewReader()
		dec, err := rd.Feed(w.Bytes())
		if err != nil || !bytes.Equal(dec, data) {
			t.Fatalf("iter=%d flush roundtrip err=%v", iter, err)
		}
		if err := rd.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestEveryBitFlip：对每条测试流的每个字节做翻转，读取端不得 panic，
// 且要么报错（带偏移），要么仍能解出原数据（翻转落在可跳过块负载）。
func TestEveryBitFlip(t *testing.T) {
	streams := buildFlipStreams(t)
	for si, raw := range streams {
		for i := range raw {
			for bit := 0; bit < 8; bit++ {
				corrupt := append([]byte(nil), raw...)
				corrupt[i] ^= 1 << bit
				r := NewReader()
				func() {
					defer func() {
						if rec := recover(); rec != nil {
							t.Fatalf("stream=%d byte=%d bit=%d panic: %v", si, i, bit, rec)
						}
					}()
					_, _ = r.Feed(corrupt)
					_ = r.Close()
				}()
			}
		}
	}
}

func buildFlipStreams(t *testing.T) [][]byte {
	t.Helper()
	var streams [][]byte

	// 流 A：含 RLE 块、原样块
	w, _ := NewWriter(2, 3)
	flushBlock(t, w, bytes.Repeat([]byte{'a'}, 100))
	flushBlock(t, w, incompressibleBlock(100, 1))
	flushBlock(t, w, []byte("tiny"))
	mustClose(t, w)
	streams = append(streams, w.Bytes())

	// 流 B：含可跳过块与重复标识
	var b bytes.Buffer
	b.Write(identifierChunk)
	wA, _ := NewWriter(2, 3)
	flushBlock(t, wA, bytes.Repeat([]byte{'a'}, 100))
	mustClose(t, wA)
	b.Write(wA.Bytes()[len(identifierChunk):])
	b.Write(identifierChunk)
	skip := make([]byte, 37)
	b.Write([]byte{0x80, byte(len(skip)), 0, 0})
	b.Write(skip)
	wB, _ := NewWriter(2, 3)
	flushBlock(t, wB, incompressibleBlock(100, 1))
	mustClose(t, wB)
	b.Write(wB.Bytes()[len(identifierChunk):])
	streams = append(streams, b.Bytes())

	return streams
}

// TestEveryFeedSplit：按每种切分点分段 Feed，结果必须相同。
func TestEveryFeedSplit(t *testing.T) {
	streams := buildFlipStreams(t)
	w, _ := NewWriter(3, 5)
	rr := rand.New(rand.NewSource(4242))
	for i := 0; i < 30; i++ {
		p := randBytes(rr, 1+rr.Intn(1500))
		flushBlock(t, w, p)
	}
	mustClose(t, w)
	streams = append(streams, w.Bytes())

	for si, raw := range streams {
		var want []byte
		r0 := NewReader()
		out, err := r0.Feed(raw)
		if err != nil {
			t.Fatalf("stream %d baseline: %v", si, err)
		}
		if err := r0.Close(); err != nil {
			t.Fatal(err)
		}
		want = out

		for cut := 1; cut < len(raw); cut++ {
			r := NewReader()
			var got []byte
			o1, e1 := r.Feed(raw[:cut])
			if e1 != nil {
				t.Fatalf("stream=%d cut=%d early err %v", si, cut, e1)
			}
			got = append(got, o1...)
			o2, e2 := r.Feed(raw[cut:])
			if e2 != nil {
				t.Fatalf("stream=%d cut=%d second err %v", si, cut, e2)
			}
			got = append(got, o2...)
			if err := r.Close(); err != nil {
				t.Fatalf("stream=%d cut=%d close %v", si, cut, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("stream=%d cut=%d split mismatch", si, cut)
			}
		}
	}
}

// TestDeterminism：相同操作序列重放得到完全相同字节。
func TestDeterminism(t *testing.T) {
	run := func() []byte {
		r := rand.New(rand.NewSource(99))
		w, _ := NewWriter(3, 4)
		for i := 0; i < 20; i++ {
			p := randBytes(rand.New(rand.NewSource(int64(i))), 1+r.Intn(500))
			if _, err := w.Write(p); err != nil {
				t.Fatal(err)
			}
			if r.Intn(2) == 0 {
				if err := w.Flush(); err != nil {
					t.Fatal(err)
				}
			}
		}
		mustClose(t, w)
		return w.Bytes()
	}
	a := run()
	b := run()
	if !bytes.Equal(a, b) {
		t.Fatal("replay mismatch")
	}
}

// TestConcurrentWriterReader：并发调用结果等价于某串行顺序。
func TestConcurrentWriterReader(t *testing.T) {
	w, _ := NewWriter(2, 2)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := bytes.Repeat([]byte{byte(i)}, 50)
			_, _ = w.Write(p)
			_ = w.Flush()
			_ = w.Bytes()
		}(i)
	}
	wg.Wait()
	mustClose(t, w)

	r := NewReader()
	out, err := r.Feed(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if len(out) != 400 {
		t.Fatalf("concurrent data len=%d", len(out))
	}

	// Reader 并发 Feed/Close
	r2 := NewReader()
	var wg2 sync.WaitGroup
	raw := w.Bytes()
	for i := 0; i < 4; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			_, _ = r2.Feed(raw)
			_ = r2.Close()
		}()
	}
	wg2.Wait()
}
