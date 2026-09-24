package main

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/pipeline"
	"ontology/sink"
)

func checkZeroWrites() error {
	p, _ := pipeline.New(cfg(&fakeClock{}), &sink.Recorder{})
	for i := 0; i < 100; i++ {
		if _, err := p.Write(nil); err != nil {
			return err
		}
	}
	st := p.Stats()
	if st.Chunks != 0 || st.Accepted != 0 || st.Closed {
		return fmt.Errorf("zero writes changed state: %+v", st)
	}
	return nil
}

func checkCloseIdempotent() error {
	rec := &sink.Recorder{}
	p, _ := pipeline.New(cfg(&fakeClock{}), rec)
	_, _ = p.Write([]byte("hello"))
	if err := p.Close(); err != nil {
		return err
	}
	if err := p.Close(); err != nil {
		return fmt.Errorf("second Close: %w", err)
	}
	if _, err := p.Write([]byte("x")); !errors.Is(err, pipeline.ErrClosed) {
		return fmt.Errorf("write after close: %v", err)
	}
	if err := drain(p); err != nil {
		return err
	}
	if n := bytes.Count(rec.Buf, []byte("0\r\n\r\n")); n != 1 {
		return fmt.Errorf("end marker appears %d times", n)
	}
	return nil
}

func checkShortWrites() error {
	want := golden(input)
	for k := 1; k <= len(want); k++ {
		sw := &sink.ShortWriter{Max: k}
		if _, err := produce(input, len(input), sw); err != nil {
			return err
		}
		if !bytes.Equal(sw.Buf, want) {
			return fmt.Errorf("split %d: bytes differ", k)
		}
	}
	return nil
}

func checkBackpressure() error {
	gate := &sink.Gate{Closed: true}
	p, _ := pipeline.New(cfg(&fakeClock{}), gate)
	if _, err := p.Write(input[:40]); err != nil {
		return err
	}
	if _, err := p.Advance(); !errors.Is(err, sink.ErrBackpressure) {
		return fmt.Errorf("advance: %v", err)
	}
	if _, err := p.Write(input[40:]); err != nil { // queued behind, not lost
		return err
	}
	_ = p.Close()
	gate.Closed = false
	if err := drain(p); err != nil {
		return err
	}
	if !bytes.Equal(gate.Buf, golden(input)) {
		return errors.New("bytes differ after backpressure")
	}
	return nil
}

func checkDisconnectResume() error {
	want := golden(input)
	for pos := 0; pos < len(want); pos++ {
		conn := &sink.Conn{Limit: pos, Per: 1}
		p, _ := pipeline.New(cfg(&fakeClock{}), conn)
		_, _ = p.Write(input)
		_ = p.Close()
		for {
			if _, err := p.Advance(); err != nil {
				break
			}
		}
		rec := &sink.Recorder{}
		q, err := pipeline.Resume(cfg(&fakeClock{}), rec, p.Checkpoint())
		if err != nil {
			return err
		}
		if err := drain(q); err != nil {
			return err
		}
		if !bytes.Equal(append(conn.Buf, rec.Buf...), want) {
			return fmt.Errorf("disconnect at %d: bytes differ", pos)
		}
	}
	return nil
}
