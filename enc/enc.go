package enc

import (
	"errors"

	"ontology/match"
	"ontology/wire"
)

const (
	DefaultWindow = 1 << 15
	DefaultChains = 32
	MaxMatch      = 1 << 20
)

type Config struct {
	Window     int
	ChainLimit int
}

type Writer struct {
	matcher *match.Matcher
	out     []byte
	pending []byte
	lit     []byte
	total   int
	flushed bool
	closed  bool
}

func New(cfg Config) (*Writer, error) {
	if cfg.Window == 0 {
		cfg.Window = DefaultWindow
	}
	if cfg.ChainLimit == 0 {
		cfg.ChainLimit = DefaultChains
	}
	m, err := match.New(cfg.Window, cfg.ChainLimit)
	if err != nil {
		return nil, err
	}
	w := &Writer{matcher: m}
	w.out = append(w.out, wire.Header()...)
	return w, nil
}

func (w *Writer) Write(data []byte) (int, error) {
	if w.closed {
		return 0, errors.New("enc: write after close")
	}
	if len(data) == 0 {
		return 0, nil
	}
	base := len(w.pending)
	w.pending = append(w.pending, data...)
	w.flushed = false
	w.drain(len(w.pending))
	_ = base
	return len(data), nil
}

func (w *Writer) Flush() error {
	if w.closed {
		return errors.New("enc: flush after close")
	}
	if w.flushed {
		return nil
	}
	w.drain(0)
	if len(w.lit) > 0 {
		w.out = wire.Literal(w.out, w.lit)
		w.lit = w.lit[:0]
	}
	w.out = wire.Flush(w.out)
	w.flushed = true
	return nil
}

func (w *Writer) Close() error {
	if w.closed {
		return errors.New("enc: close after close")
	}
	w.drain(0)
	if len(w.lit) > 0 {
		w.out = wire.Literal(w.out, w.lit)
		w.lit = w.lit[:0]
	}
	w.out = wire.End(w.out, w.total, wire.Checksum(nil))
	w.closed = true
	return nil
}

func (w *Writer) Bytes() []byte { return w.out }

func (w *Writer) drain(slack int) {
	for len(w.pending)-slack >= wire.MinMatch {
		found := w.matcher.Find(w.pending, 0, max(len(w.pending), wire.MinMatch))
		if found.Length < wire.MinMatch {
			w.lit = append(w.lit, w.pending[0])
			w.matcher.Insert(w.pending, 0)
			w.pending = w.pending[1:]
			w.total++
			continue
		}
		if found.Length == len(w.pending) && slack > 0 {
			return
		}
		if len(w.lit) > 0 {
			w.out = wire.Literal(w.out, w.lit)
			w.lit = w.lit[:0]
		}
		w.out = wire.Match(w.out, found.Distance, found.Length)
		for i := 0; i < found.Length; i++ {
			w.matcher.Insert(w.pending, i)
		}
		w.pending = w.pending[found.Length:]
		w.total += found.Length
	}
}

func Compress(data []byte, cfg Config) ([]byte, error) {
	w, err := New(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return w.Bytes(), nil
}

func CompressParallel(data []byte, blockSize, workers int, cfg Config) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, errors.New("enc: blockSize and workers must be positive")
	}
	if cfg.Window == 0 {
		cfg.Window = DefaultWindow
	}
	if cfg.ChainLimit == 0 {
		cfg.ChainLimit = DefaultChains
	}
	blocks := (len(data) + blockSize - 1) / blockSize
	parts := make([][]byte, blocks)
	sem := make(chan struct{}, workers)
	errs := make(chan error, 1)
	for i := 0; i < blocks; i++ {
		i := i
		start := i * blockSize
		end := start + blockSize
		if end > len(data) {
			end = len(data)
		}
		dictStart := start - cfg.Window
		if dictStart < 0 {
			dictStart = 0
		}
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			p, err := compressBlock(data[dictStart:start], data[start:end], cfg)
			if err != nil && len(errs) == 0 {
				errs <- err
			}
			parts[i] = p
		}()
	}
	for i := 0; i < cap(sem); i++ {
		sem <- struct{}{}
	}
	select {
	case err := <-errs:
		return nil, err
	default:
	}
	out := append([]byte(nil), wire.Header()...)
	for _, p := range parts {
		out = append(out, p...)
	}
	out = wire.End(out, len(data), wire.Checksum(data))
	return out, nil
}

func compressBlock(dict, data []byte, cfg Config) ([]byte, error) {
	m, err := match.New(cfg.Window, cfg.ChainLimit)
	if err != nil {
		return nil, err
	}
	if len(dict) > cfg.Window {
		dict = dict[len(dict)-cfg.Window:]
	}
	m.Preset(dict)
	w := Writer{matcher: m}
	w.pending = append(w.pending, data...)
	w.drain(0)
	if len(w.lit) > 0 {
		w.out = wire.Literal(w.out, w.lit)
	}
	return w.out, nil
}
