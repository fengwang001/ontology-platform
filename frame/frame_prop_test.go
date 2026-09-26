package frame

import (
	"bytes"
	"sync"
	"testing"
)

// TestSkipTouchesNoPayload proves SkipField is O(1): across m fields with
// large payloads the unexported touched-payload counter stays 0 every call,
// i.e. only each field's 4-byte prefix is read regardless of payload size.
func TestSkipTouchesNoPayload(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		fields := make([][]byte, m)
		for i := range fields {
			fields[i] = make([]byte, 4096)
		}
		r := NewReader(Encode(fields))
		touched := 0
		for j := 0; j < m; j++ {
			if err := r.SkipField(); err != nil {
				t.Fatal(err)
			}
			touched += r.skipPayloadTouched
		}
		if touched != 0 {
			t.Fatalf("m=%d skip touched %d payload bytes", m, touched)
		}
		if r.Pos() != m*(4+4096) {
			t.Fatalf("m=%d final pos %d", m, r.Pos())
		}
	}
}

// TestConcurrentEncodeDecode: concurrent decoders of one read-only buffer
// agree byte-for-byte; concurrent encoders match serial encoding. No sleep.
func TestConcurrentEncodeDecode(t *testing.T) {
	const N = 64
	src := [][]byte{[]byte("hi"), {}, []byte("world!"), []byte("A")}
	record := Encode(src)
	lists := make([][][]byte, N)
	serial := make([][]byte, N)
	for i := range lists {
		lists[i] = [][]byte{[]byte("g"), make([]byte, i), src[i%len(src)]}
		serial[i] = Encode(lists[i])
	}
	var wg sync.WaitGroup
	dec := make([][][]byte, N)
	enc := make([][]byte, N)
	barrier := make(chan struct{})
	wg.Add(2 * N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			<-barrier
			f, err := Decode(record)
			if err != nil {
				t.Errorf("decode %d: %v", i, err)
				return
			}
			dec[i] = f
		}(i)
		go func(i int) {
			defer wg.Done()
			<-barrier
			enc[i] = Encode(lists[i])
		}(i)
	}
	close(barrier)
	wg.Wait()
	for i := 0; i < N; i++ {
		if len(dec[i]) != len(src) {
			t.Fatalf("decoder %d field count", i)
		}
		for j := range src {
			if !bytes.Equal(dec[i][j], src[j]) || !bytes.Equal(enc[i], serial[i]) {
				t.Fatalf("concurrent mismatch at goroutine %d", i)
			}
		}
	}
}
