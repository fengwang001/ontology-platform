package enc

import (
	"errors"
	"sync"

	"ontology/match"
	"ontology/wire"
)

// CompressParallel splits data into fixed blocks compressed concurrently. Each
// block may reference up to Window bytes of the previous block's tail; the
// stream is byte-identical for any workers value.
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	if blockSize <= 0 {
		return nil, errors.New("enc: blockSize must be > 0")
	}
	if workers <= 0 {
		return nil, errors.New("enc: workers must be > 0")
	}
	nb := (len(data) + blockSize - 1) / blockSize
	toks := make([][]byte, nb)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				lo, hi := j*blockSize, (j+1)*blockSize
				if hi > len(data) {
					hi = len(data)
				}
				toks[j] = encodeBlock(data[max0(lo-DefaultWindow):lo], data[lo:hi])
			}
		}()
	}
	for j := 0; j < nb; j++ {
		jobs <- j
	}
	close(jobs)
	wg.Wait()
	out := wire.AppendHeader(nil, DefaultWindow)
	for _, t := range toks {
		out = append(out, t...)
	}
	out = append(out, wire.TagEnd)
	out = wire.AppendUvarint(out, uint64(len(data)))
	out = wire.AppendUvarint(out, checksum(data))
	return out, nil
}

func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
}

// encodeBlock compresses one block with dict as preset history. It uses the
// same greedy rule as the streaming encoder, so ordering cannot change bytes.
func encodeBlock(dict, block []byte) []byte {
	mt, _ := match.New(DefaultWindow, DefaultChain)
	mt.Preset(dict)
	var out []byte
	for k := 0; k < len(block); {
		mt.SetPending(block[k:])
		d, l := mt.Find(0, len(block)-k)
		if l == 0 {
			out = wire.AppendLiteral(out, block[k:k+1])
			l = 1
		} else {
			out = wire.AppendMatch(out, d, l)
		}
	mt.Advance(l)
		k += l
	}
	return out
}

func checksum(p []byte) uint64 {
	h := uint64(fnvOffset)
	for _, b := range p {
		h ^= uint64(b)
		h *= fnvPrime
	}
	return h
}
