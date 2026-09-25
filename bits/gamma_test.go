package bits_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/api"
	"ontology/bits"
	"ontology/gcode"
)

func gen() [][]int64 {
	s := [][]int64{{}, {1}, {1, 2, 3, 5, 8}, {1 << 62}, {1<<40 - 1, 1 << 40}}
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 24; i++ {
		v := make([]int64, 1+r.Intn(40))
		for j := range v {
			v[j] = 1 + r.Int63n(1<<50)
		}
		s = append(s, v)
	}
	return s
}

func TestReference(t *testing.T) { // 不变量 1：与朴素参照逐字节/逐元素相同
	for _, v := range gen() {
		want := bits.NaiveStreamEncode(v)
		got, err := gcode.Encode(v)
		if err != nil || string(got) != string(want) {
			t.Fatalf("enc %v: %x want %x", v, got, want)
		}
		if len(v) == 0 && got == nil {
			t.Fatal("Encode([]) must be non-nil []byte{}, not nil")
		}
		nv, k := bits.NaiveDecode(want)
		d, e := gcode.Decode(want)
		if k != 0 || e != nil || fmt.Sprint(d) != fmt.Sprint(nv) {
			t.Fatalf("dec %v: %v vs %v (k=%d)", v, d, nv, k)
		}
	}
}

func TestStreamChunks(t *testing.T) { // 不变量 2：切分点无关（遍历每个字节切点）
	for _, v := range gen()[1:] {
		b, _ := gcode.Encode(v)
		want, _ := gcode.Decode(b)
		for cut := 0; cut <= len(b); cut++ {
			sd := gcode.NewStreamDecoder()
			a, e1 := sd.Feed(b[:cut])
			c, e2 := sd.Feed(b[cut:])
			if e1 != nil || e2 != nil || sd.End() != nil || fmt.Sprint(append(a, c...)) != fmt.Sprint(want) {
				t.Fatalf("cut %d: %v %v %v", cut, e1, e2, sd.End())
			}
		}
	}
	sd := gcode.NewStreamDecoder() // 未竟码 Feed 期不报错，End 才判截断
	if _, e := sd.Feed([]byte{0x01}); e != nil || !errors.Is(sd.End(), gcode.ErrTruncated) {
		t.Fatal("pending 0x01 must truncate at End")
	}
}

func TestDeterminism(t *testing.T) { // 不变量 3：确定性（无前缀见内部 TestPrefixFree）
	for _, v := range gen() {
		a, _ := gcode.Encode(v)
		b, _ := gcode.Encode(v)
		if string(a) != string(b) {
			t.Fatal("non-deterministic encoding")
		}
	}
}

func TestAtomicFailure(t *testing.T) { // 不变量 4：失败整体 nil、哨兵互不相同
	badP := [][]byte{{0x01}, {0x81}, {0, 0, 0, 0, 0, 0, 0, 1}}
	badE := []error{gcode.ErrTruncated, gcode.ErrTruncated, gcode.ErrOverflow}
	for i := range badP {
		d, err := gcode.Decode(badP[i])
		if !errors.Is(err, badE[i]) || d != nil {
			t.Fatalf("Decode(%x)=%v,%v want nil,%v", badP[i], d, err, badE[i])
		}
	}
	if b, e := gcode.Encode([]int64{1, 0}); e == nil || b != nil {
		t.Fatal("zero accepted")
	}
	if b, e := gcode.Encode([]int64{-7}); !errors.Is(e, gcode.ErrNonPositive) || b != nil {
		t.Fatalf("negative accepted: %x %v", b, e)
	}
	if gcode.ErrTruncated == gcode.ErrOverflow || gcode.ErrTruncated == gcode.ErrNonPositive {
		t.Fatal("sentinels not distinct")
	}
	if d, e := gcode.Decode([]byte{0xA6, 0x51, 0x00}); e != nil || fmt.Sprint(d) != "[1 2 3 5 8]" {
		t.Fatalf("Decode broken after failure: %v %v", d, e)
	}
}

func TestComplexityCounter(t *testing.T) { // 解 2^40 恰检查 81 位，不随 m 增长
	prev := -1
	for _, m := range []int{100, 1000, 10000} {
		v := make([]int64, m+1)
		for i := 0; i < m; i++ {
			v[i] = 1
		}
		v[m] = 1 << 40
		b, _ := gcode.Encode(v)
		r := bits.NewReader(b)
		for i := 0; i < m; i++ {
			if n, ok, e := gcode.DecodeOne(r); e != nil || !ok || n != 1 {
				t.Fatalf("prefix %d: %d %v %v", i, n, ok, e)
			}
		}
		n, ok, e := gcode.DecodeOne(r)
		if got := bits.LastChecks(r); e != nil || !ok || n != 1<<40 || got != 81 || (prev >= 0 && got != prev) {
			t.Fatalf("m=%d n=%d checks=%d want 81", m, n, got)
		}
		prev = bits.LastChecks(r)
	}
}

func TestConcurrent(t *testing.T) { // 并发结果须与串行一致（go test -race）
	in := []int64{1, 2, 3, 5, 8, 1 << 40, 1 << 62, 777}
	sb, _ := gcode.Encode(in)
	res := make(chan bool, 16)
	for g := 0; g < 16; g++ {
		go func() {
			b, e1 := gcode.Encode(in)
			d, e2 := gcode.Decode(b)
			res <- e1 == nil && e2 == nil && string(b) == string(sb) && fmt.Sprint(d) == fmt.Sprint(in)
		}()
	}
	for g := 0; g < 16; g++ {
		if !<-res {
			t.Fatal("concurrent diverged")
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if e := api.SelfCheck(); e != nil {
		t.Fatalf("SelfCheck: %v", e)
	}
	b, _ := api.Encode([]int64{1, 2, 3, 5, 8})
	d, e := api.Decode(b)
	if e != nil || string(b) != "\xA6\x51\x00" || fmt.Sprint(d) != "[1 2 3 5 8]" ||
		api.ErrTruncated != gcode.ErrTruncated || api.ErrOverflow != gcode.ErrOverflow ||
		api.ErrNonPositive != gcode.ErrNonPositive {
		t.Fatalf("api wrappers/reexport: %x %v %v", b, d, e)
	}
}
