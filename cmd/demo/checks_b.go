package main

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/pipeline"
	"ontology/sink"
	"ontology/sizeline"
)

func checkChunkSeq() error {
	var want []int
	for _, split := range []int{1, 3, 7, len(input)} {
		sizes, err := produce(input, split, &sink.Recorder{})
		if err != nil {
			return err
		}
		if want == nil {
			want = sizes
		} else if !equalInts(sizes, want) {
			return fmt.Errorf("split %d: chunk sizes %v != %v", split, sizes, want)
		}
	}
	return nil
}

func checkExtEscaping() error {
	c := cfg(&fakeClock{})
	c.Exts = []sizeline.Ext{{Key: "weird", Val: `a;b="c"\d`}}
	rec := &sink.Recorder{}
	p, err := pipeline.New(c, rec)
	if err != nil {
		return err
	}
	_, _ = p.Write([]byte("payload-bytes"))
	_ = p.Close()
	if err := drain(p); err != nil {
		return err
	}
	line := rec.Buf[:bytes.Index(rec.Buf, []byte("\r\n"))]
	_, exts, err := sizeline.Decode(line)
	if err != nil {
		return err
	}
	if len(exts) != 1 || exts[0] != c.Exts[0] {
		return fmt.Errorf("got %+v", exts)
	}
	return nil
}

func checkLimits() error {
	c := cfg(&fakeClock{})
	c.MaxWriteBytes = 8
	p, _ := pipeline.New(c, &sink.Recorder{})
	before := p.Stats()
	if _, err := p.Write(make([]byte, 9)); !errors.Is(err, pipeline.ErrWriteTooLarge) {
		return fmt.Errorf("write too large: %v", err)
	}
	c2 := cfg(&fakeClock{})
	c2.MaxExtBytes = 2
	if _, err := pipeline.New(c2, &sink.Recorder{}); !errors.Is(err, pipeline.ErrExtsTooLarge) {
		return fmt.Errorf("exts too large: %v", err)
	}
	c3 := cfg(&fakeClock{})
	c3.MaxBufferBytes = 32
	q, _ := pipeline.New(c3, &sink.Recorder{})
	for {
		_, err := q.Write([]byte("0123456789abcdef"))
		if errors.Is(err, pipeline.ErrWouldBlock) {
			break
		}
		if err != nil {
			return err
		}
	}
	if p.Stats() != before {
		return errors.New("rejection changed state")
	}
	return nil
}

func checkConcurrency() error {
	p, _ := pipeline.New(cfg(&fakeClock{}), &sink.ShortWriter{Max: 5})
	var bad atomic.Bool
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, err := p.Write([]byte("12345678")); err != nil {
					bad.Store(true)
				}
			}
		}()
	}
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = p.Advance()
				st := p.Stats()
				if st.Accepted != st.Confirmed+st.Pending {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	_ = p.Close()
	if err := drain(p); err != nil {
		return err
	}
	if bad.Load() {
		return errors.New("identity violated under concurrency")
	}
	st := p.Stats()
	if st.Accepted != st.Confirmed+st.Pending {
		return fmt.Errorf("final identity broken: %+v", st)
	}
	return nil
}

func checkScanBytes() error {
	scan := func(n int) int64 {
		c := cfg(&fakeClock{})
		c.MaxChunk = 64
		p, _ := pipeline.New(c, &sink.ShortWriter{Max: 3})
		for written := 0; written < n; {
			m := 4096
			if n-written < m {
				m = n - written
			}
			_, _ = p.Write(make([]byte, m))
			written += m
		}
		for i := 0; i < 5; i++ {
			_, _ = p.Advance()
		}
		return p.ScanBytes()
	}
	s1, s2 := scan(1<<10), scan(1<<20)
	if s1 != s2 {
		return fmt.Errorf("scan grows with N: 1KB=%d 1MB=%d", s1, s2)
	}
	return nil
}
