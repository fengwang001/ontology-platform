package enc

import (
	"errors"
	"sync"

	"hash/crc32"

	"ontology/match"
	"ontology/wire"
)

func crc32IEEE(data []byte) uint64 {
	return uint64(crc32.ChecksumIEEE(data))
}

// compressBlock compresses one block as a pure function of its own bytes and
// the previous block's bytes (only the final Window bytes are visible).
func compressBlock(prev, block []byte, windowCap, chain int) []byte {
	m := match.New(windowCap, chain)
	if len(prev) > 0 {
		start := len(prev) - windowCap
		if start < 0 {
			start = 0
		}
		m.Preset(prev[start:])
	}
	var out []byte
	litStart := 0
	emitLit := func(end int) {
		if end > litStart {
			out = append(out, wire.Literal(uint64(end-litStart))...)
			out = append(out, block[litStart:end]...)
		}
	}
	for i := 0; i < len(block); {
		if i+wire.MinMatch > len(block) {
			break
		}
		if d, l := m.Find(i, block); l >= wire.MinMatch {
			emitLit(i)
			out = append(out, wire.Match(uint64(d), uint64(l))...)
			for k := 1; k < l; k++ {
				var b1, b2 byte
				if i+k+1 < len(block) {
					b1 = block[i+k+1]
				}
				if i+k+2 < len(block) {
					b2 = block[i+k+2]
				}
				m.Insert(block[i+k], b1, b2)
			}
			i += l
			litStart = i
			continue
		}
		i++
	}
	emitLit(len(block))
	return out
}

// CompressParallel compresses data in independent blocks using up to workers
// goroutines. Output is byte-identical for any worker count.
func CompressParallel(data []byte, blockSize, workers int, cfg Config) ([]byte, error) {
	if cfg.Window <= 0 || cfg.Chain <= 0 || blockSize <= 0 || workers <= 0 {
		return nil, errors.New("enc: invalid parallel parameters")
	}
	n := (len(data) + blockSize - 1) / blockSize
	if n == 0 {
		n = 1
	}
	parts := make([][]byte, n)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for b := 0; b < n; b++ {
		start := b * blockSize
		end := start + blockSize
		if end > len(data) {
			end = len(data)
		}
		var prev []byte
		if start-cfg.Window >= 0 {
			prev = data[start-cfg.Window : start]
		} else {
			prev = data[:start]
		}
		block := data[start:end]
		wg.Add(1)
		sem <- struct{}{}
		go func(b int, prev, block []byte) {
			defer wg.Done()
			parts[b] = compressBlock(prev, block, cfg.Window, cfg.Chain)
			<-sem
		}(b, prev, block)
	}
	wg.Wait()
	out := wire.Header(uint64(cfg.Window))
	for _, p := range parts {
		out = append(out, p...)
	}
	out = append(out, wire.End(uint64(len(data)), crc32IEEE(data))...)
	return out, nil
}
