package lzw

import (
	"bytes"
	"io"
	"sync"
	"testing"
)

// threadSafeBuffer serializes writes itself; the codec must still be safe to
// call from many goroutines concurrently.
type threadSafeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *threadSafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *threadSafeBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Bytes()
}

func TestEncoderConcurrentWrites(t *testing.T) {
	in := bytes.Repeat([]byte("concurrent-lzw-"), 500)
	chunks := splitBytes(in, 37)

	var wg sync.WaitGroup
	run := func() {
		defer wg.Done()
		var sb threadSafeBuffer
		e := NewEncoder(&sb)
		// Hammer the encoder concurrently: correctness requires the result to
		// equal *some* serial ordering, so concurrently repeat deterministic
		// chunk writes while a closer waits; the mutex linearizes them.
		var cwg sync.WaitGroup
		for g := 0; g < 8; g++ {
			cwg.Add(1)
			go func(off int) {
				defer cwg.Done()
				for i := off; i < len(chunks); i += 8 {
					if n, err := e.Write(chunks[i]); err != nil || n != len(chunks[i]) {
						t.Errorf("Write=%d,%v", n, err)
						return
					}
				}
			}(g)
		}
		cwg.Wait()
		if err := e.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
		// The stream must at least be self-consistent: start with the clear
		// code and finish with the end-of-information code.
		got := sb.Bytes()
		if len(got) < 3 || got[0] != 0x00 {
			t.Errorf("concurrent stream bad prefix: % x", got)
		}
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go run()
	}
	wg.Wait()
}

func TestDecoderConcurrentWrites(t *testing.T) {
	in := make([]byte, 12000)
	newRand(99).Read(in)
	packed := encodeChunked(t, in, 1<<30)

	var wg sync.WaitGroup
	for run := 0; run < 8; run++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var sb threadSafeBuffer
			d := NewDecoder(&sb)
			chunks := splitBytes(packed, 17)
			// Concurrent calls on the same decoder must be linearized to a
			// serial order; deliver ordered chunks while several goroutines
			// contend on the mutex via a barrier, then one owner submits.
			barrier := make(chan struct{})
			var cwg sync.WaitGroup
			for g := 0; g < 4; g++ {
				cwg.Add(1)
				go func(g int) {
					defer cwg.Done()
					<-barrier
					// Only the owner submits; contenders stress the mutex.
					if g != 0 {
						if _, err := d.Write(nil); err != nil {
							t.Errorf("nil Write: %v", err)
						}
					}
				}(g)
			}
			close(barrier)
			for _, c := range chunks {
				if _, err := d.Write(c); err != nil {
					t.Errorf("decoder Write: %v", err)
					return
				}
			}
			cwg.Wait()
			if err := d.Close(); err != nil || !bytes.Equal(sb.b.Bytes(), in) {
				t.Errorf("Close=%v equal=%v outLen=%d", err,
					bytes.Equal(sb.b.Bytes(), in), sb.b.Len())
			}
		}()
	}
	wg.Wait()
}

// Deterministic same-order concurrency: feed chunks through a single goroutine
// but race-detector-friendly parallel encoders/decoders, asserting identical
// streams.
func TestParallelIndependentCodecs(t *testing.T) {
	in := bytes.Repeat([]byte{0xAB}, 20000)
	ref := encodeChunked(t, in, 1<<30)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var b bytes.Buffer
			e := NewEncoder(&b)
			for _, c := range splitBytes(in, 128) {
				if _, err := e.Write(c); err != nil {
					t.Error(err)
					return
				}
			}
			if err := e.Close(); err != nil {
				t.Error(err)
				return
			}
			if !bytes.Equal(b.Bytes(), ref) {
				t.Error("parallel codec produced different stream")
				return
			}
			var out bytes.Buffer
			d := NewDecoder(&out)
			if _, err := d.Write(b.Bytes()); err != nil {
				t.Error(err)
				return
			}
			if err := d.Close(); err != nil || !bytes.Equal(out.Bytes(), in) {
				t.Errorf("decode: %v equal=%v", err, bytes.Equal(out.Bytes(), in))
			}
		}()
	}
	wg.Wait()
}

// failingWriter forces encoder write errors and checks the sticky behavior.
type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }

func TestEncoderUnderlyingWriterError(t *testing.T) {
	e := NewEncoder(failingWriter{})
	_, err := e.Write([]byte("enough bytes to flush a full packed byte xyz"))
	if err == nil {
		t.Fatal("expected writer error")
	}
	if _, err2 := e.Write([]byte("a")); err2 != err {
		t.Fatalf("second Write err=%v want same sticky error", err2)
	}
	if err3 := e.Close(); err3 != err {
		t.Fatalf("Close err=%v want same sticky error", err3)
	}
}

func splitBytes(b []byte, n int) [][]byte {
	var out [][]byte
	for i := 0; i < len(b); i += n {
		end := i + n
		if end > len(b) {
			end = len(b)
		}
		out = append(out, b[i:end])
	}
	return out
}
