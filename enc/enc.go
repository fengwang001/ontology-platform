// Package enc is a streaming LZ77O compressor with parallel block mode.
package enc

import (
	"errors"
	"sync"

	"ontology/match"
	"ontology/wire"
)

// CompressParallel compresses data in fixed-size blocks using up to workers
// goroutines. Each block may back-reference the previous block's final
// WindowCap bytes. Output depends only on data and blockSize, never on
// worker count or scheduling, and is decodable by the streaming decoder.
func CompressParallel(data []byte, blockSize, workers int) ([]byte, error) {
	cfg, err := Config{WindowCap: 1 << 15, ChainLimit: 32}.withDefaults()
	_ = err
	if blockSize <= 0 {
		return nil, errors.New("enc: blockSize must be positive")
	}
	if workers <= 0 {
		return nil, errors.New("enc: workers must be positive")
	}
	nb := (len(data) + blockSize - 1) / blockSize
	if nb == 0 {
		nb = 1
	}
	type job struct {
		i     int
		toks  []match.Token
		block []byte
	}
	jobs := make([]job, nb)
	for i := range jobs {
		st := i * blockSize
		en := st + blockSize
		if en > len(data) {
			en = len(data)
		}
		jobs[i].i, jobs[i].block = i, data[st:en]
	}
	jobsCh := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobsCh {
				var tail []byte
				if jobs[i].i > 0 {
					st := jobs[i].i * blockSize
					t0 := st - cfg.WindowCap
					if t0 < 0 {
						t0 = 0
					}
					tail = data[t0:st]
				}
				jobs[i].toks = encodeBlock(cfg, tail, jobs[i].block)
			}
		}()
	}
	for i := 0; i < nb; i++ {
		jobsCh <- i
	}
	close(jobsCh)
	wg.Wait()

	out := []byte(wire.Magic)
	out = append(out, wire.Version)
	var total uint64
	var sum = uint64(wire.FNVOffset64)
	for i := range jobs {
		for _, t := range jobs[i].toks {
			if t.Length == 0 {
				out = append(out, wire.TagLiteral)
				out = wire.PutVarint(out, uint64(len(t.Lit)))
				out = append(out, t.Lit...)
				continue
			}
			out = append(out, wire.TagMatch)
			out = wire.PutVarint(out, uint64(t.Distance))
			out = wire.PutVarint(out, uint64(t.Length))
		}
		out = append(out, wire.TagFlush)
		total += uint64(len(jobs[i].block))
		for _, c := range jobs[i].block {
			sum ^= uint64(c)
			sum *= wire.FNVPrime64
		}
	}
	out = append(out, wire.TagEnd)
	out = wire.PutVarint(out, total)
	out = wire.PutVarint(out, sum)
	return out, nil
}

// Config configures a compressor.
type Config struct {
	WindowCap  int // history ring size; must be > 0
	ChainLimit int // max chain probes per position; must be > 0
	NiceLen    int // stop match search at this length; 0 => 128
}

func (c Config) withDefaults() (Config, error) {
	if c.WindowCap <= 0 {
		return c, errors.New("enc: WindowCap must be positive")
	}
	if c.ChainLimit <= 0 {
		return c, errors.New("enc: ChainLimit must be positive")
	}
	if c.NiceLen <= 0 {
		c.NiceLen = 128
	}
	return c, nil
}

// Writer compresses a stream. Pending input is held until Flush/Close so
// output never depends on Write boundaries. A Writer is not safe for
// concurrent use.
type Writer struct {
	cfg      Config
	pending  []byte
	out      []byte
	emitted  uint64 // original bytes already finalized (history)
	checksum uint64
	closed   bool
	head     bool // header emitted
	finder   *match.Finder
}

// NewWriter validates cfg and returns a compressor.
func NewWriter(cfg Config) (*Writer, error) {
	c, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	f, err := match.New(match.Config{c.WindowCap, c.ChainLimit, c.NiceLen})
	if err != nil {
		return nil, err
	}
	return &Writer{cfg: c, finder: f, checksum: wire.Checksum(nil)}, nil
}

// Write buffers p. Compression is deferred to Flush/Close.
func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("enc: write after close")
	}
	w.pending = append(w.pending, p...)
	return len(p), nil
}

func (w *Writer) emitHeader() {
	w.out = append(w.out, wire.Magic...)
	w.out = append(w.out, wire.Version)
}

func (w *Writer) emitTokens(toks []match.Token) {
	for _, t := range toks {
		if t.Length == 0 {
			w.out = append(w.out, wire.TagLiteral)
			w.out = wire.PutVarint(w.out, uint64(len(t.Lit)))
			w.out = append(w.out, t.Lit...)
			continue
		}
		w.out = append(w.out, wire.TagMatch)
		w.out = wire.PutVarint(w.out, uint64(t.Distance))
		w.out = wire.PutVarint(w.out, uint64(t.Length))
	}
}

// Flush finalizes all pending input and emits a flush marker when it
// produced records. A second Flush with no new input emits nothing.
func (w *Writer) Flush() error {
	if w.closed {
		return errors.New("enc: flush after close")
	}
	if len(w.pending) == 0 {
		return nil
	}
	if !w.head {
		w.emitHeader()
	}
	toks := w.finder.Encode(nil, w.pending)
	w.emitTokens(toks)
	w.out = append(w.out, wire.TagFlush)
	w.checksum = fnvContinue(w.checksum, w.pending)
	w.emitted += uint64(len(w.pending))
	w.head = true
	w.pending = w.pending[:0]
	return nil
}

// Close emits the stream end. Empty input still yields header + end.
func (w *Writer) Close() error {
	if w.closed {
		return errors.New("enc: close after close")
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if !w.head {
		w.emitHeader()
	}
	w.out = append(w.out, wire.TagEnd)
	w.out = wire.PutVarint(w.out, w.emitted)
	w.out = wire.PutVarint(w.out, w.checksum)
	w.closed = true
	return nil
}

// Bytes returns the compressed bytes produced so far.
func (w *Writer) Bytes() []byte { return w.out }

func fnvContinue(h uint64, p []byte) uint64 {
	for _, c := range p {
		h ^= uint64(c)
		h *= wire.FNVPrime64
	}
	return h
}

// encodeBlock compresses block using the previous block tail as preset.
func encodeBlock(cfg Config, prevTail, block []byte) []match.Token {
	f, _ := match.New(match.Config{cfg.WindowCap, cfg.ChainLimit, cfg.NiceLen})
	return f.Encode(prevTail, block)
}
