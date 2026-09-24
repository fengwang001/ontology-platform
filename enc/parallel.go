package enc

import (
	"hash/crc32"
	"sync"

	"ontology/wire"
)

// CompressParallel compresses data in fixed blocks using workers goroutines.
// Block k seeds with up to WindowSize trailing bytes of block k-1, so its
// result depends only on its own bytes and that read-only prefix; output is
// byte-identical for any worker count.
func CompressParallel(data []byte, blockSize, workers int, cfg Config) ([]byte, error) {
	if cfg.WindowSize <= 0 || cfg.ChainLimit <= 0 || blockSize <= 0 || workers <= 0 {
		return nil, ErrConfig
	}
	nb := (len(data) + blockSize - 1) / blockSize
	if nb == 0 {
		nb = 1
	}
	parts := make([][]byte, nb)
	jobs := make(chan int, nb)
	for k := 0; k < nb; k++ {
		jobs <- k
	}
	close(jobs)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range jobs {
				lo := k * blockSize
				hi := lo + blockSize
				if hi > len(data) {
					hi = len(data)
				}
				dlo := 0
				if lo > cfg.WindowSize {
					dlo = lo - cfg.WindowSize
				}
				b := newBlock(cfg.WindowSize, cfg.ChainLimit, minMatch)
				b.seed(data[dlo:lo])
				parts[k] = b.run(data[lo:hi])
			}
		}()
	}
	wg.Wait()
	out := wire.AppendHeader(nil, uint64(cfg.WindowSize))
	for _, q := range parts {
		out = append(out, q...)
}
	return wire.AppendEnd(out, uint64(len(data)), uint64(crc32.ChecksumIEEE(data))), nil
}
