package fragmentlog

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
)

func makeRecord(g, i int) []byte {
	n := (i*7 + g*13) % 120
	rec := make([]byte, n)
	for j := range rec {
		rec[j] = byte(g*31 + i*17 + j)
	}
	return rec
}

// TestConcurrentAppendRead hammers a Log with concurrent appends and
// readers. The committed bytes must equal some serialisation of the
// appends, per-goroutine record order must be preserved, and every
// appended record must be readable at the offset Append returned.
func TestConcurrentAppendRead(t *testing.T) {
	const (
		B         = 64
		writers   = 8
		perWriter = 200
	)
	l, err := NewLog(B)
	if err != nil {
		t.Fatal(err)
	}
	type appended struct {
		off int64
		rec []byte
	}
	per := make([][]appended, writers)
	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				rec := makeRecord(g, i)
				off, err := l.Append(rec)
				if err != nil {
					t.Errorf("append: %v", err)
					return
				}
				per[g] = append(per[g], appended{off, rec})
			}
		}(g)
	}
	// Concurrent readers while appends are in flight: they must only ever
	// see clean record sequences (no corruption), ending in io.EOF.
	var readWg sync.WaitGroup
	for r := 0; r < 4; r++ {
		readWg.Add(1)
		go func() {
			defer readWg.Done()
			rd := l.Reader()
			for {
				_, _, err := rd.Next()
				if errors.Is(err, io.EOF) {
					return
				}
				if err != nil {
					t.Errorf("concurrent read saw corruption: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	readWg.Wait()
	// Drain the final state.
	rd := l.Reader()
	byOff := map[int64][]byte{}
	for {
		rec, off, err := rd.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("final read: %v", err)
		}
		if _, dup := byOff[off]; dup {
			t.Fatalf("duplicate record offset %d", off)
		}
		byOff[off] = rec
	}
	if len(byOff) != writers*perWriter {
		t.Fatalf("read %d records, want %d", len(byOff), writers*perWriter)
	}
	// Every appended record is present at its own offset, and each
	// goroutine's records keep their append order.
	for g := 0; g < writers; g++ {
		prev := int64(-1)
		for i, a := range per[g] {
			got, ok := byOff[a.off]
			if !ok || !bytes.Equal(got, a.rec) {
				t.Fatalf("goroutine %d record %d missing/wrong at offset %d", g, i, a.off)
			}
			if a.off <= prev {
				t.Fatalf("goroutine %d: offsets not increasing at record %d", g, i)
			}
			prev = a.off
		}
	}
	t.Logf("%d 个写协程并发追加 %d 条记录: 字节流等价于某个串行顺序, 全部记录按各自偏移读回一致",
		writers, writers*perWriter)
}

// TestConcurrentWriterAppends checks that a single Writer used from many
// goroutines produces a clean, decodable stream.
func TestConcurrentWriterAppends(t *testing.T) {
	const (
		B         = 32
		writers   = 6
		perWriter = 150
	)
	var buf bytes.Buffer
	w, err := NewWriter(&buf, B)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := w.Append(makeRecord(g, i)); err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	rd := mustReader(t, buf.Bytes(), B)
	count := 0
	for {
		_, _, err := rd.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		count++
	}
	if count != writers*perWriter {
		t.Fatalf("read %d records, want %d", count, writers*perWriter)
	}
	t.Logf("单 Writer 被 %d 协程并发追加: 读回 %d 条记录, 无损坏", writers, count)
}
