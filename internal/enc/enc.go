package enc

import (
	"hash/crc32"
	"io"
	"sync"

	"ontology/internal/match"
	"ontology/internal/wire"
)

const DefaultWindow = 1 << 16
const DefaultChain = 32
const maxMatch = 1 << 15

func DefaultConfig() wire.Config {
	return wire.Config{WindowCap: DefaultWindow, MaxChain: DefaultChain}
}

type Writer struct {
	cfg    wire.Config
	out    io.Writer
	pend   []byte
	crc    uint32
	total  int
	match  *match.Matcher
	closed bool
}

func NewWriter(w io.Writer, cfg wire.Config) (*Writer, error) {
	if w == nil || !cfg.Valid() {
		return nil, wire.ErrConfig
	}
	if _, err := w.Write(wire.Header(cfg)); err != nil {
		return nil, err
	}
	return &Writer{cfg: cfg, out: w, match: match.New(cfg.WindowCap, cfg.MaxChain)}, nil
}

func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, wire.ErrConfig
	}
	w.pend = append(w.pend, p...)
	return len(p), nil
}

func (w *Writer) Flush() error {
	if w.closed || len(w.pend) == 0 {
		return nil
	}
	b, n := compressBlock(w.pend, nil, w.match)
	if _, err := w.out.Write(b); err != nil {
		return err
	}
	b = []byte{wire.TagFlush}
	if _, err := w.out.Write(b); err != nil {
		return err
	}
	w.crc = crc32.Update(w.crc, crc32.IEEETable, w.pend[:n])
	w.total += n
	w.pend = w.pend[n:]
	return nil
}

func (w *Writer) Close() error {
	if w.closed {
		return wire.ErrConfig
	}
	w.closed = true
	b, n := compressBlock(w.pend, nil, w.match)
	if _, err := w.out.Write(b); err != nil {
		return err
	}
	w.crc = crc32.Update(w.crc, crc32.IEEETable, w.pend[:n])
	w.total += len(w.pend)
	b = []byte{wire.TagEnd}
	b = wire.AppendUvarint(b, uint64(w.total))
	b = wire.AppendUvarint(b, uint64(w.crc))
	_, err := w.out.Write(b)
	w.pend = nil
	return err
}

func CompressParallel(data []byte, blockSize, workers int, cfg wire.Config) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 || !cfg.Valid() {
		return nil, wire.ErrConfig
	}
	n := (len(data) + blockSize - 1) / blockSize
	if n == 0 {
		n = 1
	}
	type job struct{ i int }
	type result struct {
		i   int
		b   []byte
		crc uint32
	}
	jobs, results := make(chan job), make(chan result, n)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				start, end := j.i*blockSize, (j.i+1)*blockSize
				if end > len(data) {
					end = len(data)
				}
				var dict []byte
				if start > 0 {
					lo := start - cfg.WindowCap
					if lo < 0 {
						lo = 0
					}
					dict = data[lo:start]
				}
				m := match.New(cfg.WindowCap, cfg.MaxChain)
				b, _ := compressBlock(data[start:end], dict, m)
				results <- result{j.i, b, crc32.ChecksumIEEE(data[start:end])}
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- job{i}
	}
	close(jobs)
	go func() { wg.Wait(); close(results) }()
	ordered := make([][]byte, n)
	crcs := make([]uint32, n)
	for r := range results {
		ordered[r.i], crcs[r.i] = r.b, r.crc
	}
	out := wire.Header(cfg)
	var crc uint32
	for i, b := range ordered {
		out = append(out, b...)
		crc = crc32.Update(crc, crc32.IEEETable, data[i*blockSize:min((i+1)*blockSize, len(data))])
	}
	out = append(out, wire.TagEnd)
	out = wire.AppendUvarint(out, uint64(len(data)))
	out = wire.AppendUvarint(out, uint64(crc))
	return out, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func compressBlock(data, dict []byte, m *match.Matcher) ([]byte, int) {
	if len(dict) > 0 {
		m.Preset(dict)
	}
	m.Load(data)
	dst := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		distance, length := m.Find(i)
		if length >= match.MinLength {
			if length > maxMatch {
				length = maxMatch
			}
			dst = append(dst, wire.TagMatch)
			dst = wire.AppendUvarint(dst, uint64(distance))
			dst = wire.AppendUvarint(dst, uint64(length))
			m.Advance(length)
			i += length
			continue
		}
		j := i
		for j < len(data) {
			_, l := m.Find(j)
			if l >= match.MinLength {
				break
			}
			if j-i < maxMatch {
				j++
			} else {
				break
			}
		}
		if j == i {
			j++
		}
		dst = append(dst, wire.TagLiteral)
		dst = wire.AppendUvarint(dst, uint64(j-i))
		dst = append(dst, data[i:j]...)
		m.Advance(j - i)
		i = j
	}
	return dst, len(data)
}
