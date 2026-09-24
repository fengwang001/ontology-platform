package serve_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"testing"

	"ontology/multipart"
	"ontology/serve"
	"ontology/source"
)

var testData = []byte("0123456789abcdefghijklmnopqrstuvwxyz")

func drain(a *serve.Assembler, chunk int) []byte {
	var out []byte
	buf := make([]byte, chunk)
	for {
		n, err := a.Write(buf)
		out = append(out, buf[:n]...)
		if err == io.EOF {
			return out
		}
	}
}

type shrinkSrc struct {
	*source.Scripted
	reads int
}

func (s *shrinkSrc) ReadAt(p []byte, off int64) (int, error) {
	s.reads++
	if s.reads == 2 {
		s.SetLen(3) // 读到一半改变长度
	}
	return s.Scripted.ReadAt(p, off)
}

func TestReadFull(t *testing.T) {
	short := source.NewScripted(testData)
	short.SetMaxChunk(1)
	a := serve.New(serve.Config{})
	if err := a.Prepare("bytes=2-8,20-25", short); err != nil {
		t.Fatalf("short reads: %v", err)
	}
	if body := drain(a, 1<<20); !bytes.Contains(body, testData[2:9]) {
		t.Errorf("short read body wrong")
	}
	boom := errors.New("boom")
	errSrc := source.NewScripted(testData)
	errSrc.SetErr(boom)
	if err := serve.New(serve.Config{}).Prepare("bytes=0-3", errSrc); !errors.Is(err, boom) {
		t.Errorf("read error not propagated: %v", err)
	}
	lie := source.NewScripted(testData[:10])
	lie.SetLen(20)
	shrink := &shrinkSrc{Scripted: source.NewScripted(testData)}
	shrink.SetMaxChunk(4)
	for name, src := range map[string]source.Source{"lie": lie, "shrink": shrink} {
		err := serve.New(serve.Config{}).Prepare("bytes=0-19", src)
		if !errors.Is(err, serve.ErrShortRead) || errors.Is(err, io.EOF) {
			t.Errorf("%s: want distinguishable ErrShortRead, got %v", name, err)
		}
	}
}

func TestWriteCutPoints(t *testing.T) {
	mk := func() *serve.Assembler {
		a := serve.New(serve.Config{BoundaryGen: func() string { return "TESTBOUNDARY" }})
		if err := a.Prepare("bytes=0-9,20-29", source.Bytes(testData)); err != nil {
			t.Fatal(err)
		}
		return a
	}
	ref := drain(mk(), 1<<20)
	for cut := 1; cut <= len(ref); cut++ {
		if got := drain(mk(), cut); !bytes.Equal(got, ref) {
			t.Fatalf("cut=%d: body differs", cut)
		}
	}
	a := mk()
	drain(a, 1<<20)
	if n, err := a.Write(make([]byte, 8)); n != 0 || err != io.EOF {
		t.Errorf("after EOF: got n=%d err=%v", n, err)
	}
}

func TestSingleRangeRaw(t *testing.T) {
	one := serve.New(serve.Config{})
	if err := one.Prepare("bytes=2-5", source.Bytes(testData)); err != nil {
		t.Fatal(err)
	}
	if one.Info().Multipart || !bytes.Equal(drain(one, 3), testData[2:6]) {
		t.Errorf("single range must be raw bytes")
	}
	deg := serve.New(serve.Config{})
	if err := deg.Prepare("bytes=0-3,4-7", source.Bytes(testData)); err != nil {
		t.Fatal(err)
	}
	if deg.Info().Multipart || !bytes.Equal(drain(deg, 1<<20), testData[0:8]) {
		t.Errorf("merged-to-one must degenerate to raw bytes")
	}
}

func TestLimitsAndStableState(t *testing.T) {
	a := serve.New(serve.Config{MaxRanges: 2, MaxBytes: 16, MaxBoundaryTries: 2,
		BoundaryGen: func() string { return "X" }})
	if err := a.Prepare("bytes=0-3", source.Bytes(testData)); err != nil {
		t.Fatal(err)
	}
	before := a.Info()
	tricky := append([]byte("\r\n--X"), testData...)
	e1 := a.Prepare("bytes=0-1,2-3,4-5", source.Bytes(testData))
	e2 := a.Prepare("bytes=0-9,20-29", source.Bytes(tricky))
	e3 := a.Prepare("bytes=0-30", source.Bytes(testData))
	if !errors.Is(e1, serve.ErrTooManyRanges) || errors.Is(e1, serve.ErrTooLarge) ||
		!errors.Is(e2, multipart.ErrBoundaryRetries) || errors.Is(e2, serve.ErrTooManyRanges) ||
		!errors.Is(e3, serve.ErrTooLarge) || errors.Is(e3, multipart.ErrBoundaryRetries) {
		t.Errorf("limit errors not distinguishable: %v %v %v", e1, e2, e3)
	}
	if !reflect.DeepEqual(a.Info(), before) {
		t.Errorf("rejected Prepare changed state")
	}
}

func TestInfoStable(t *testing.T) {
	a := serve.New(serve.Config{})
	if !reflect.DeepEqual(a.Info(), serve.Info{}) || !reflect.DeepEqual(a.Info(), a.Info()) {
		t.Errorf("unprepared Info not zero/stable")
	}
	if err := a.Prepare("bytes=0-3,8-11", source.Bytes(testData)); err != nil {
		t.Fatal(err)
	}
	i1, i2 := a.Info(), a.Info()
	if !reflect.DeepEqual(i1, i2) || i1.Written != 0 || !i1.Multipart || len(i1.Ranges) != 2 {
		t.Errorf("Info unstable or wrong: %+v", i1)
	}
	drain(a, 5)
	if got := a.Info().Written; got != i1.TotalBytes {
		t.Errorf("Written=%d, want %d", got, i1.TotalBytes)
	}
}

func TestConcurrentDistinct(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			data := bytes.Repeat([]byte{byte('a' + g%26)}, 100)
			a := serve.New(serve.Config{BoundaryGen: func() string { return fmt.Sprintf("B%d", g) }})
			if err := a.Prepare("bytes=0-9,50-59", source.Bytes(data)); err != nil {
				errs <- err
				return
			}
			if got := drain(a, 7); !bytes.Contains(got, data[:10]) || !bytes.Contains(got, data[50:60]) {
				errs <- fmt.Errorf("goroutine %d: wrong body", g)
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
