package stream_test

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/addr"
	"ontology/split"
	"ontology/store"
	"ontology/stream"
)

var cfg = split.Config{Min: 256, Max: 4096, Window: 16}

func prng(n int, seed uint32) []byte {
	d := make([]byte, n)
	x := seed
	for i := range d {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		d[i] = byte(x>>9) + byte(i)
	}
	return d
}

// feed 按 plan 给出的每片字节数循环切分喂入（plan 元素可大于剩余长度）。
func feed(t *testing.T, data, plan []byte, lim store.Limits) *stream.Streamer {
	t.Helper()
	st, err := stream.New(cfg, lim)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) == 1 && plan[0] == 1 {
		for i := range data {
			if err := st.Write(data[i : i+1]); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		pos, pi := 0, 0
		for pos < len(data) {
			n := int(plan[pi%len(plan)])
			pi++
			if n <= 0 || n > len(data)-pos {
				n = len(data) - pos
			}
			if err := st.Write(data[pos : pos+n]); err != nil {
				t.Fatal(err)
			}
			pos += n
		}
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	return st
}

func plans(n int) [][]byte {
	return [][]byte{{1}, {byte(n)}, {byte(n/3 + 1), byte(n/3 + 2)}, {7, 250, 4097, 2}}
}

// 不变量 1 + 3：写入切法无关 + 重组逐字节相同。
func TestWriteSplitIndependenceAndReassemble(t *testing.T) {
	for _, n := range []int{1, 200, 1000, 12345} {
		data := prng(n, 11)
		var ref []stream.Chunk
		for ci, plan := range plans(n) {
			st := feed(t, data, plan, store.Limits{})
			got := st.Chunks()
			if ci == 0 {
				ref = got
			} else if len(got) != len(ref) {
				t.Fatalf("n=%d count %d vs %d", n, len(got), len(ref))
			} else {
				for i := range got {
					if got[i] != ref[i] {
						t.Fatalf("n=%d chunk %d %+v != %+v", n, i, got[i], ref[i])
					}
				}
			}
			out, err := st.Reassemble()
			if err != nil || !bytes.Equal(out, data) {
				t.Fatalf("n=%d reassemble mismatch", n)
			}
			if err := st.SelfCheck(); err != nil {
				t.Fatalf("n=%d selfcheck: %v", n, err)
			}
		}
	}
}

// 空流 / 零长写入可区分；长度 < min 只有一块短块。
func TestEmptyZeroAndShort(t *testing.T) {
	empty, _ := stream.New(cfg, store.Limits{})
	if err := empty.Flush(); err != nil || len(empty.Chunks()) != 0 || empty.Stats().Writes != 0 {
		t.Fatal("empty stream must have zero chunks and zero writes")
	}
	zw, _ := stream.New(cfg, store.Limits{})
	if err := zw.Write(nil); err != nil || zw.Stats().ZeroWrites != 1 {
		t.Fatal("zero-length Write must be recorded")
	}
	if err := zw.Flush(); err != nil || len(zw.Chunks()) != 0 {
		t.Fatal("zero-length writes add no chunks")
	}
	short := feed(t, prng(100, 5), []byte{100}, store.Limits{})
	if len(short.Chunks()) != 1 {
		t.Fatalf("stream < min must be one short block, got %d", len(short.Chunks()))
	}
	if err := short.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 不变量 4：内容相同块同一地址、引用计数正确。
func TestDedupAcrossStream(t *testing.T) {
	unit := prng(3000, 9)
	rep := append(append(append([]byte{}, unit...), unit...), unit...)
	st := feed(t, rep, []byte{1}, store.Limits{})
	if st.Store().BlockCount() == len(st.Chunks()) {
		t.Fatal("identical blocks should dedup")
	}
	if err := st.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 不变量 2：中间插入 128 字节，受影响块数 ≤ 64 且与流长无关。
func TestLocality(t *testing.T) {
	for _, n := range []int{60000, 120000, 240000} {
		orig := prng(n, 21)
		mod := append(append(append([]byte{}, orig[:n/2]...), prng(128, 22)...), orig[n/2:]...)
		s1 := feed(t, orig, []byte{37}, store.Limits{}).Chunks()
		s2 := feed(t, mod, []byte{37}, store.Limits{}).Chunks()
		i, j := len(s1), len(s2)
		for i > 0 && j > 0 && s1[i-1].Addr == s2[j-1].Addr {
			i, j = i-1, j-1
		}
		if j > 64 {
			t.Fatalf("n=%d affected=%d > 64", n, j)
		}
	}
}

// 推进次数：两档流长成正比、系数 ≤ 1；逐字节与整片次数相同（无重复推进）。
func TestAdvanceCount(t *testing.T) {
	for _, n := range []int{100000, 1000000} {
		data := prng(n, 33)
		a := feed(t, data, []byte{1}, store.Limits{})
		b := feed(t, data, []byte{byte(n)}, store.Limits{})
		av, bv := a.Stats().Advances, b.Stats().Advances
		if av != bv || av > n {
			t.Fatalf("n=%d advances byte=%d bulk=%d", n, av, bv)
		}
		fmt.Printf("advances n=%d: %d (coef %.4f)\n", n, av, float64(av)/float64(n))
	}
}

// 不变量 5：四类错误 + 拒绝不留痕 + 拒绝后仍可用。
func TestRejections(t *testing.T) {
	if _, err := stream.New(split.Config{Min: 9, Max: 8, Window: 2}, store.Limits{}); !errors.Is(err, split.ErrMinMax) {
		t.Fatalf("min>max: %v", err)
	}
	if _, err := stream.New(split.Config{Min: 4, Max: 8, Window: 9}, store.Limits{}); !errors.Is(err, split.ErrWindow) {
		t.Fatalf("window: %v", err)
	}
	st, _ := stream.New(cfg, store.Limits{MaxBytes: 10})
	if err := st.Write(prng(4096, 1)); !errors.Is(err, store.ErrTooManyBytes) {
		t.Fatalf("byte cap: %v", err)
	}
	if err := st.SelfCheck(); err != nil {
		t.Fatalf("dirty after reject: %v", err)
	}
	if err := st.Write(prng(300, 7)); err != nil || st.Flush() != nil {
		t.Fatal("streamer must remain usable after rejection")
	}
	good, _ := stream.New(cfg, store.Limits{})
	if _, err := good.Get(addr.Of([]byte("definitely-absent"))); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing address must be ErrNotFound: %v", err)
	}
}

// 并发只读重组：N 个 goroutine 结果逐字节相同。
func TestConcurrentReassemble(t *testing.T) {
	data := prng(200000, 77)
	st := feed(t, data, []byte{37}, store.Limits{})
	const N = 16
	var wg sync.WaitGroup
	results := make([][]byte, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = st.Reassemble()
		}(i)
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil || !bytes.Equal(results[i], data) || !bytes.Equal(results[i], results[0]) {
			t.Fatalf("goroutine %d mismatch", i)
		}
	}
}
