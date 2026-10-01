package hdlc

import (
	"bytes"
	"sync"
	"testing"
)

func TestConcurrentEncoderAndDecoder(t *testing.T) {
	payloads := [][]byte{{0x9F}, {0xFF, 0xFF}, []byte("abc")}

	var encoded bytes.Buffer
	encoder := NewEncoder(&encoded, 16)
	var rejected bytes.Buffer
	rejectEncoder := NewEncoder(&rejected, 2)

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, payload := range payloads {
				var local bytes.Buffer
				localEncoder := NewEncoder(&local, 16)
				if err := localEncoder.WriteFrame(payload); err != nil {
					t.Errorf("local WriteFrame: %v", err)
					return
				}
				if err := localEncoder.Flush(); err != nil {
					t.Errorf("local Flush: %v", err)
					return
				}
				localDecoder := NewDecoder()
				frames, err := localDecoder.Write(local.Bytes())
				if err != nil {
					t.Errorf("local decoder Write: %v", err)
					return
				}
				for _, frame := range frames {
					if !frameSlicesEqual([][]byte{frame}, [][]byte{payload}) {
						t.Errorf("frame=%x, want %x", frame, payload)
					}
				}
			}

			if err := rejectEncoder.WriteFrame(nil); err != ErrEmptyPayload {
				t.Errorf("concurrent empty WriteFrame: %v", err)
			}
			if rejected.Len() != 0 {
				t.Errorf("rejected output length=%d, want 0", rejected.Len())
			}
		}()
	}

	for _, payload := range payloads {
		if err := encoder.WriteFrame(payload); err != nil {
			t.Fatalf("shared encoder WriteFrame: %v", err)
		}
	}
	if err := encoder.Flush(); err != nil {
		t.Fatalf("shared encoder Flush: %v", err)
	}

	wg.Wait()

	decoder := NewDecoder()
	frames, err := decoder.Write(encoded.Bytes())
	if err != nil {
		t.Fatalf("final decoder Write: %v", err)
	}
	stats := decoder.Stats()
	if stats.Frames != uint64(len(frames)) {
		t.Fatalf("final frames=%d but frame counter=%d", len(frames), stats.Frames)
	}
}
