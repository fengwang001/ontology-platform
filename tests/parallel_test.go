package tests

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"ontology/dec"
	"ontology/enc"
	"ontology/match"
	"ontology/window"
)

func driveCandidates(data []byte, chain int) int64 {
	win, _ := window.New(testWindow)
	m, _ := match.New(win, chain)
	m.Append(data)
	for pos := 0; pos < len(data); {
		if _, l := m.Find(pos); l >= match.MinLen {
			pos += l
		} else {
			pos++
		}
	}
	return m.Candidates()
}

func TestComplexity(t *testing.T) {
	const chain = 32
	sizes := []int{64 << 10, 4 << 20}
	perByte := make([]float64, 2)
	for i, n := range sizes {
		for _, data := range [][]byte{bytes.Repeat([]byte{0x5A}, n), randBytes(n, int64(n))} {
			if c := driveCandidates(data, chain); c > int64(chain)*int64(n) {
				t.Fatalf("n=%d: 候选 %d 超过 %d*n", n, c, chain)
			}
		}
		perByte[i] = float64(driveCandidates(bytes.Repeat([]byte{0x5A}, n), chain)) / float64(n)
	}
	if perByte[0] > 2*perByte[1] || perByte[1] > 2*perByte[0] {
		t.Fatalf("每字节考察数随规模增长: %v", perByte)
	}
	stream := compress(t, bytes.Repeat([]byte{0x5A}, 4<<20), 1<<22)
	if len(stream) >= 64<<10 {
		t.Fatalf("4MB 全同字节压缩后 %d 字节，未用长回指", len(stream))
	}
	t.Logf("per-byte candidates 64KB=%.6f 4MB=%.6f; 4MB compressed=%dB", perByte[0], perByte[1], len(stream))
}

func TestParallel(t *testing.T) {
	data := append(bytes.Repeat([]byte("ontology-platform-"), 20000), randBytes(1<<20, 7)...)
	data = data[:1<<20]
	var ref []byte
	for _, w := range []int{1, 2, 4, 8} {
		got, err := enc.CompressParallel(data, 32768, w)
		if err != nil {
			t.Fatal(err)
		}
		if ref == nil {
			ref = got
		} else if !bytes.Equal(ref, got) {
			t.Fatalf("workers=%d 输出不同", w)
		}
	}
	if out, err := decompress(ref, 0); err != nil || !bytes.Equal(out, data) {
		t.Fatalf("并行流往返失败: %v", err)
	}
	for i := 0; i < 30; i++ {
		if got, _ := enc.CompressParallel(data, 32768, 8); !bytes.Equal(ref, got) {
			t.Fatal("第 30 次重复压缩输出不同")
		}
	}
}

func TestConcurrentDecoders(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		data := randBytes(2000, int64(100+i))
		stream := compress(t, data, 7)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if out, err := decompress(stream, 0); err != nil || !bytes.Equal(out, data) {
				errs <- fmt.Errorf("decoder: %v", err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestBadConfig(t *testing.T) {
	if _, err := enc.New(&bytes.Buffer{}, enc.Config{Window: -1}); err == nil {
		t.Fatal("窗口 -1 未拒绝")
	}
	if _, err := enc.New(&bytes.Buffer{}, enc.Config{MaxChain: -1}); err == nil {
		t.Fatal("链长 -1 未拒绝")
	}
	if _, err := dec.New(dec.Config{Window: -1}); err == nil {
		t.Fatal("解压窗口 -1 未拒绝")
	}
	if _, err := enc.CompressParallel([]byte("x"), 0, 1); err == nil {
		t.Fatal("blockSize 0 未拒绝")
	}
}
