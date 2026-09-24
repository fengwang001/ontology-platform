package serve

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"

	"ontology/multipart"
	"ontology/source"
)

var errBoom = errors.New("boom")

type zeros struct{} // 无限零字节随机源，用于确定性复现边界串

func (zeros) Read(p []byte) (int, error) { return len(p), nil }

func data(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

func build(t *testing.T, src *source.Mem, hdr string) *Assembler {
	t.Helper()
	a := New(src, Limits{})
	if err := a.Build(hdr); err != nil {
		t.Fatal(err)
	}
	return a
}

func writeAll(a *Assembler, chunk int) []byte {
	var out []byte
	buf := make([]byte, chunk)
	for {
		n, err := a.Write(buf)
		out = append(out, buf[:n]...)
		if err == io.EOF {
			return out
		}
		if err != nil {
			panic(err)
		}
	}
}

func TestShortReadFillAndErrors(t *testing.T) {
	d := data(100)
	a := build(t, &source.Mem{Data: d, MaxChunk: 3}, "bytes=10-29") // 短读源
	if got := writeAll(a, 64); !bytes.Equal(got, d[10:30]) {
		t.Fatalf("short read fill: got %d bytes, want 20", len(got))
	}
	// 读到一半长度变化：报告 100 实际只有 50，EOF 不足可判定
	short := New(&source.Mem{Data: d[:50], SizeFunc: func(int64) int64 { return 100 }}, Limits{})
	if err := short.Build("bytes=40-99"); !errors.Is(err, ErrShortRead) {
		t.Fatalf("want ErrShortRead, got %v", err)
	}
	// 注入读错误：与 ErrShortRead 可判定
	boom := New(&source.Mem{Data: d, MaxChunk: 3, Err: errBoom, ErrAfter: 5}, Limits{})
	if err := boom.Build("bytes=0-19"); !errors.Is(err, errBoom) || errors.Is(err, ErrShortRead) {
		t.Fatalf("want errBoom distinct from ErrShortRead, got %v", err)
	}
}

func TestAllSplitPointsIdentical(t *testing.T) {
	d := data(200)
	hdr := "bytes=10-29, 50-69, 180-"
	a := build(t, &source.Mem{Data: d}, hdr)
	want := writeAll(a, 1<<20)
	for chunk := 1; chunk <= len(want); chunk++ { // 遍历所有切分点
		a.Rewind()
		if got := writeAll(a, chunk); !bytes.Equal(got, want) {
			t.Fatalf("chunk=%d: bytes differ", chunk)
		}
	}
}

func TestSingleRangeBareAndDegenerate(t *testing.T) {
	d := data(50)
	a := build(t, &source.Mem{Data: d}, "bytes=2-5")
	if a.Stats().Multipart {
		t.Fatal("single range must not be encapsulated")
	}
	if got := writeAll(a, 64); !bytes.Equal(got, d[2:6]) {
		t.Fatalf("bare body = %v, want %v", got, d[2:6])
	}
	// 两个相邻区间合并成一个后应退化为裸字节
	b := build(t, &source.Mem{Data: d}, "bytes=0-4, 5-9")
	if st := b.Stats(); st.Multipart || len(st.Ranges) != 1 {
		t.Fatalf("merged pair must degenerate to bare: %+v", st)
	}
	if got := writeAll(b, 64); !bytes.Equal(got, d[0:10]) {
		t.Fatalf("degenerate body = %v, want %v", got, d[0:10])
	}
}

func TestBoundaryAvoidsContent(t *testing.T) {
	fake := multipart.Prefix + "00000000000000000000000000000000"
	d := []byte("HEAD" + fake + "MID" + fake + "TAIL")
	a := build(t, &source.Mem{Data: d}, "bytes=0-10, 20-")
	if !a.Stats().Multipart {
		t.Fatal("want multipart")
	}
	body := writeAll(a, 1<<20)
	boundary := body[2:bytes.IndexByte(body, '\r')]
	if bytes.Contains(d, boundary) {
		t.Fatalf("boundary %q appears in content", boundary)
	}
	if !bytes.Contains(body, d[0:11]) || !bytes.Contains(body, d[20:]) {
		t.Fatal("content bytes not intact in body")
	}
}

func TestLimitsRejectWithoutStateChange(t *testing.T) {
	d := data(100)
	cases := []struct {
		name string
		lim  Limits
		hdr  string
		src  *source.Mem
		want error
	}{
		{"too many ranges", Limits{MaxRanges: 2}, "bytes=0-1, 5-6, 10-11", &source.Mem{Data: d}, ErrTooManyRanges},
		{"too many bytes", Limits{MaxBytes: 5}, "bytes=0-20", &source.Mem{Data: d}, ErrTooManyBytes},
		{"boundary tries", Limits{MaxTries: 1, Rand: zeros{}}, "bytes=0-44, 50-60",
			&source.Mem{Data: []byte(multipart.Prefix + "00000000000000000000000000000000" + "0123456789ABCDEFGHIJ")}, ErrBoundaryTries},
	}
	for _, c := range cases {
		a := New(c.src, c.lim)
		before := a.Stats()
		if err := a.Build(c.hdr); !errors.Is(err, c.want) {
			t.Errorf("%s: want %v, got %v", c.name, c.want, err)
		}
		if after := a.Stats(); !reflect.DeepEqual(before, after) {
			t.Errorf("%s: state changed after rejection: %+v -> %+v", c.name, before, after)
		}
		if n, err := a.Write(make([]byte, 8)); n != 0 || err != io.EOF {
			t.Errorf("%s: rejected assembler must not write", c.name)
		}
	}
}

func TestStatsStableAndZeroValue(t *testing.T) {
	a := New(&source.Mem{Data: data(30)}, Limits{})
	if z := a.Stats(); z.Total != 0 || z.Written != 0 || z.Multipart || z.Ranges != nil {
		t.Fatalf("not zero value before build: %+v", z)
	}
	if err := a.Build("bytes=0-9, 20-29"); err != nil {
		t.Fatal(err)
	}
	s1, s2 := a.Stats(), a.Stats()
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("two queries differ: %+v vs %+v", s1, s2)
	}
	if s1.Written != 0 || !s1.Multipart || len(s1.Ranges) != 2 || s1.Total == 0 {
		t.Fatalf("bad stats: %+v", s1)
	}
	s1.Ranges[0].Start = 999 // 改快照不影响内部状态
	if a.Stats().Ranges[0].Start == 999 {
		t.Fatal("stats must be a copy")
	}
}

func TestConcurrentIndependentAssemblers(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			d := data(64 + g)
			hdr := "bytes=0-9, 20-29, 40-"
			a := New(&source.Mem{Data: d, MaxChunk: 2 + g%3}, Limits{Rand: zeros{}})
			ref := New(&source.Mem{Data: d}, Limits{Rand: zeros{}})
			if err := errors.Join(a.Build(hdr), ref.Build(hdr)); err != nil {
				errs <- err
				return
			}
			got := writeAll(a, 1+g)
			if want := writeAll(ref, 1<<20); !bytes.Equal(got, want) {
				errs <- errors.New("concurrent assemblies interfere")
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
