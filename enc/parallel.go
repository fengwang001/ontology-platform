package enc

import (
	"bytes"
	"io"
	"runtime"
	"sync"

	"ontology/match"
	"ontology/wire"
)

// CompressParallel splits data into blocks preset with the previous tail.
// Output is one ordinary stream, identical for any positive workers value.
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize <= 0 {
		return nil, ErrConfig
	}
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	nb := max(1, (len(data)+blockSize-1)/blockSize)
	parts := make([][]byte, nb)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				parts[i] = compressBlock(data, i, blockSize)
			}
		}()
	}
	for i := range nb {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	out := append([]byte(nil), wire.Header...)
	for _, p := range parts {
		out = append(out, p...)
	}
	out = wire.AppendVarint(append(out, wire.TagEnd), uint64(len(data)))
	out = wire.AppendVarint(out, wire.Checksum(wire.InitialChecksum, data))
	return out, nil
}

func compressBlock(data []byte, idx, blockSize int) []byte {
	start, end := idx*blockSize, min(len(data), (idx+1)*blockSize)
	dictStart := max(0, start-defaultWindowCap)
	c, _ := NewWriter(io.Discard, nil)
	var buf bytes.Buffer
	c.w = &buf
	c.win.AddAll(data[dictStart:start])
	c.m.InsertRange(1, data[dictStart:start])
	c.pos = len(data[dictStart:start])
	c.pending = append(c.pending, data[start:end]...)
	_ = c.process(true)
	return buf.Bytes()
}

var _ = match.MinMatch
