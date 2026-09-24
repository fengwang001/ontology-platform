package pipeline

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"

	"ontology/sink"
)

// Every short-write split point must yield the exact golden byte stream.
func TestShortWriteAllSplitPoints(t *testing.T) {
	cfg := testCfg()
	want := golden(t, cfg, testInput)
	for k := 1; k <= len(want); k++ {
		sw := &sink.ShortWriter{Max: k}
		produce(t, cfg, testInput, sw)
		if !bytes.Equal(sw.Buf, want) {
			t.Fatalf("split %d: byte stream differs", k)
		}
	}
}

// Backpressure must neither lose nor reorder data, and writes accepted
// during backpressure must queue behind earlier bytes.
func TestBackpressureKeepsData(t *testing.T) {
	cfg := testCfg()
	gate := &sink.Gate{Closed: true}
	p := mustNew(t, cfg, gate)
	third := len(testInput) / 3
	for _, part := range [][]byte{testInput[:third], testInput[third : 2*third], testInput[2*third:]} {
		if _, err := p.Write(part); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Advance(); !errors.Is(err, sink.ErrBackpressure) {
			t.Fatalf("advance under backpressure: %v", err)
		}
		if len(gate.Buf) != 0 {
			t.Fatal("sink received bytes while closed")
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	gate.Closed = false
	drain(t, p)
	if !bytes.Equal(gate.Buf, golden(t, cfg, testInput)) {
		t.Fatal("byte stream after backpressure differs from golden")
	}
}

// Disconnect at every byte position, then resume from the checkpoint:
// prefix received before the break plus resumed stream must equal golden.
func TestDisconnectResumeAllPositions(t *testing.T) {
	cfg := testCfg()
	half := len(testInput) / 2
	prefix := func() int {
		rec := &sink.Recorder{}
		p := mustNew(t, cfg, rec)
		if _, err := p.Write(testInput[:half]); err != nil {
			t.Fatal(err)
		}
		drain(t, p)
		return len(rec.Buf)
	}()
	want := golden(t, cfg, testInput)
	for pos := 0; pos < prefix; pos++ {
		conn := &sink.Conn{Limit: pos, Per: 1}
		p := mustNew(t, cfg, conn)
		if _, err := p.Write(testInput[:half]); err != nil {
			t.Fatal(err)
		}
		for {
			if _, err := p.Advance(); err != nil {
				if !errors.Is(err, sink.ErrDisconnected) {
					t.Fatalf("pos %d: %v", pos, err)
				}
				break
			}
		}
		rec := &sink.Recorder{}
		q, err := Resume(cfg, rec, p.Checkpoint())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.Write(testInput[half:]); err != nil {
			t.Fatal(err)
		}
		if err := q.Close(); err != nil {
			t.Fatal(err)
		}
		drain(t, q)
		got := append(conn.Buf, rec.Buf...)
		if !bytes.Equal(got, want) {
			t.Fatalf("disconnect at %d: stream differs", pos)
		}
	}
}

// Chunk sizes must depend only on the byte stream and clock, never on how
// upstream splits its Write calls.
func TestChunkSequenceCallAgnostic(t *testing.T) {
	cfg := testCfg()
	cfg.Window = time.Hour                         // fixed fake clock: only MaxChunk cuts
	input := bytes.Repeat([]byte("0123456789"), 4) // 40 bytes
	want := []int{16, 16, 8}
	for split := 1; split <= len(input); split++ {
		p := mustNew(t, cfg, &sink.Recorder{})
		for i := 0; i < len(input); i += split {
			end := i + split
			if end > len(input) {
				end = len(input)
			}
			if _, err := p.Write(input[i:end]); err != nil {
				t.Fatal(err)
			}
		}
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
		if got := p.ChunkSizes(); !reflect.DeepEqual(got, want) {
			t.Fatalf("split %d: chunk sizes %v, want %v", split, got, want)
		}
		if st := p.Stats(); st.Chunks != int64(len(want)) {
			t.Fatalf("split %d: chunks = %d", split, st.Chunks)
		}
	}
}
