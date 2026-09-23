// Command demo 逐条演示分块流式写出管线的语义，不读参数、不联网。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/chunker"
	"ontology/pipeline"
	"ontology/sink"
	"ontology/sizeline"
)

type check struct {
	name string
	fn   func() error
}

func main() {
	ref := reference()
	checks := []check{
		{"零长写入不产块", checkZeroWrite},
		{"Close 幂等结束块一次", checkCloseIdempotent},
		{"短写切分点遍历一致", checkShortWrites},
		{"背压不丢数据", checkBackpressure},
		{"断开续传一致", checkDisconnect},
		{"块序列与调用边界无关", checkChunkBoundaries},
		{"扩展转义可读回", checkExtensions},
		{"三类超限与背压", checkLimits},
		{"恒等式并发成立", checkConcurrency},
		{"扫描字节数两组对照", checkScanBytes},
	}
	_ = ref
	pass := 0
	for _, c := range checks {
		if err := c.fn(); err != nil {
			fmt.Printf("FAIL %-22s %v\n", c.name, err)
			continue
		}
		pass++
		fmt.Printf("OK   %s\n", c.name)
	}
	fmt.Printf("总计 %d/%d 通过\n", pass, len(checks))
	if pass != len(checks) {
		os.Exit(1)
	}
}

func checkZeroWrite() error {
	snk := sink.Controlled{}
	p, _ := newPipe(&snk)
	for range 100 {
		if _, err := p.Write(nil); err != nil {
			return err
		}
	}
	s := p.Stats()
	if s.Chunks != 0 || s.Closed || snk.Len() != 0 {
		return fmt.Errorf("chunks=%d closed=%v sent=%d", s.Chunks, s.Closed, snk.Len())
	}
	return nil
}

func checkCloseIdempotent() error {
	snk := sink.Controlled{}
	p, _ := newPipe(&snk)
	feed(p, []byte("abc"))
	if err := p.Close(); err != nil || p.Close() != nil {
		return errors.New("close not idempotent")
	}
	if got := bytes.Count(snk.Bytes(), endFrame); got != 1 {
		return fmt.Errorf("end frame count=%d", got)
	}
	if _, err := p.Write([]byte("x")); !errors.Is(err, pipeline.ErrClosed) {
		return fmt.Errorf("write after close err=%v", err)
	}
	return nil
}

func checkShortWrites() error {
	ref := reference()
	for k := 1; k <= len(ref); k++ {
		snk := sink.Controlled{MaxPerWrite: k}
		p, _ := newPipe(&snk)
		feed(p, payload)
		if err := p.Close(); err != nil {
			return err
		}
		if !bytes.Equal(snk.Bytes(), ref) {
			return fmt.Errorf("short write k=%d mismatch", k)
		}
	}
	return nil
}

func checkBackpressure() error {
	snk := sink.Controlled{Backpressured: true}
	p, _ := newPipe(&snk)
	feed(p, payload[:5], payload[5:])
	if snk.Len() != 0 {
		return errors.New("backpressure leaked bytes")
	}
	snk.SetBackpressure(false)
	if err := p.Close(); err != nil {
		return err
	}
	drain(p)
	if !bytes.Equal(snk.Bytes(), reference()) {
		return errors.New("bytes reordered or lost")
	}
	return nil
}

func checkDisconnect() error {
	ref := reference()
	for pos := 0; pos <= len(ref); pos++ {
		s1 := sink.Controlled{Limit: pos}
		p1, _ := newPipe(&s1)
		feed(p1, payload)
		if err := p1.Close(); err != nil && !errors.Is(err, sink.ErrDisconnected) {
			return err
		}
		cp, frames := p1.Checkpoint()
		s2 := sink.Controlled{}
		p2, err := pipeline.Resume(baseCfg(), &s2, cp, frames)
		if err != nil {
			return err
		}
		drain(p2)
		got := append(s1.Bytes(), s2.Bytes()...)
		if !bytes.Equal(got, ref) {
			return fmt.Errorf("resume pos=%d mismatch", pos)
		}
	}
	return nil
}

func checkChunkBoundaries() error {
	data := []byte("0123456789abcdefghijklmnopqrstu") // 31 字节
	splits := [][][]byte{
		{data},
		{data[:1], data[1:]},
		{data[:7], data[7:20], data[20:]},
	}
	var want []int
	for si, parts := range splits {
		snk := sink.Controlled{}
		p, clk := newPipe(&snk)
		feed(p, parts...)
		clk.Advance(window + 1)
		if err := p.Advance(); err != nil {
			return err
		}
		if err := p.Close(); err != nil {
			return err
		}
		sizes, err := parseDataSizes(snk.Bytes())
		if err != nil {
			return err
		}
		if si == 0 {
			want = sizes
		} else if !equalInts(sizes, want) {
			return fmt.Errorf("split %d sizes=%v want=%v", si, sizes, want)
		}
	}
	return nil
}

func checkExtensions() error {
	exts := []chunker.Ext{
		{Key: "a", Value: "semi;colon"},
		{Key: "b", Value: "eq=ual"},
		{Key: "c", Value: "quo\"te\\and\r\n"},
	}
	snk := sink.Controlled{}
	cfg := baseCfg()
	cfg.MaxChunk = 64
	cfg.Exts = exts
	p, err := pipeline.New(cfg, &snk)
	if err != nil {
		return err
	}
	feed(p, []byte("payload"))
	if err := p.Close(); err != nil {
		return err
	}
	line, _, err := firstLine(snk.Bytes())
	if err != nil {
		return err
	}
	size, got, err := sizeline.ParseLine(line)
	if err != nil || size != 7 || len(got) != 3 {
		return fmt.Errorf("parse size=%d n=%d err=%v", size, len(got), err)
	}
	for i := range exts {
		if got[i] != (sizeline.Ext{Key: exts[i].Key, Value: exts[i].Value}) {
			return fmt.Errorf("ext %d mismatch: %q=%q", i, got[i].Key, got[i].Value)
		}
	}
	return nil
}

func checkLimits() error {
	cases := []struct {
		setup func(*pipeline.Config)
		want  error
	}{
		{func(c *pipeline.Config) { c.MaxWrite = 4 }, pipeline.ErrChunkTooLarge},
		{func(c *pipeline.Config) { c.MaxExtLen = 2 }, pipeline.ErrExtTooLarge},
		{func(c *pipeline.Config) { c.MaxPending = 8 }, pipeline.ErrBackpressure},
	}
	for _, tc := range cases {
		snk := sink.Controlled{Backpressured: true}
		cfg := baseCfg()
		cfg.Exts = []chunker.Ext{{Key: "x", Value: "longvalue"}}
		tc.setup(&cfg)
		p, err := pipeline.New(cfg, &snk)
		if err != nil {
			return err
		}
		before := p.Stats()
		if _, err = p.Write([]byte("abcdefgh")); !errors.Is(err, tc.want) {
			return fmt.Errorf("want %v got %v", tc.want, err)
		}
		if after := p.Stats(); after != before {
			return fmt.Errorf("%v changed state %+v -> %+v", tc.want, before, after)
		}
	}
	return nil
}

func checkConcurrency() error {
	snk := sink.Controlled{MaxPerWrite: 7}
	p, _ := newPipe(&snk)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	stopDone := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				if _, err := p.Write([]byte("hello")); err != nil &&
					!errors.Is(err, sink.ErrBackpressure) {
					panic(err)
				}
			}
		}()
	}
	go func() {
		for {
			select {
			case <-stop:
				close(stopDone)
				return
			default:
				s := p.Stats()
				if s.Accepted != s.Confirmed+s.Pending {
					panic("invariant violated")
				}
				_ = p.Advance()
			}
		}
	}()
	wg.Wait()
	close(stop)
	<-stopDone
	if err := p.Close(); err != nil {
		return err
	}
	drain(p)
	s := p.Stats()
	if s.Accepted != s.Confirmed+s.Pending || snk.Len() != int(s.Confirmed) {
		return fmt.Errorf("invariant end: %+v sent=%d", s, snk.Len())
	}
	return nil
}

func checkScanBytes() error {
	var deltas []int64
	for _, n := range []int{1 << 10, 1 << 20} {
		snk := sink.Controlled{Backpressured: true}
		cfg := baseCfg()
		cfg.MaxChunk = 64
		p, err := pipeline.New(cfg, &snk)
		if err != nil {
			return err
		}
		if _, err = p.Write(make([]byte, n)); err != nil {
			return err
		}
		before := p.ScanBytes()
		snk.Backpressured, snk.PartialStall, snk.MaxPerWrite = false, true, 16
		if err = p.Advance(); err != nil && !errors.Is(err, sink.ErrBackpressure) {
			return err
		}
		deltas = append(deltas, p.ScanBytes()-before)
	}
	if deltas[0] != deltas[1] || deltas[0] <= 16 {
		return fmt.Errorf("scan deltas=%v (want equal, >16)", deltas)
	}
	return nil
}
