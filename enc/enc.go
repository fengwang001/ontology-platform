// Package enc implements the streaming LZ77 compressor and deterministic
// parallel block compression.
package enc

import (
	"bytes"
	"errors"

	"ontology/match"
	"ontology/wire"
)

// Config configures a compressor.
type Config struct {
	Window     int
	ChainLimit int
}

// DefaultConfig returns window 32768 and chain limit 32.
func DefaultConfig() Config { return Config{Window: 32768, ChainLimit: 32} }

// Writer compresses a byte stream. It is not safe for concurrent use.
type Writer struct {
	cfg Config
	m   *match.Matcher
	out bytes.Buffer

	data  []byte // bytes pushed to matcher, indexed from 0 for this stream
	front int    // committed frontier (emitted) into data
	lit   []byte // literal bytes accumulated before the next record

	mStart int // pending match start offset in data (-1 if none)
	md     int
	ml     int

	total  uint64
	closed bool
}

// NewWriter constructs a streaming compressor that emits its own header.
func NewWriter(cfg Config) (*Writer, error) {
	m, err := match.New(cfg.Window, cfg.ChainLimit)
	if err != nil {
		return nil, err
	}
	w := &Writer{cfg: cfg, m: m, mStart: -1}
	w.out.Write(wire.Magic)
	w.out = wire.AppendVarint(w.out, uint64(cfg.Window))
	return w, nil
}

// Bytes returns compressed output produced so far.
func (w *Writer) Bytes() []byte { return w.out.Bytes() }

// Total returns accepted input bytes.
func (w *Writer) Total() uint64 { return w.total }

// Write feeds input; records are emitted only when matches are capped by a
// following mismatch, keeping output independent of write boundaries.
func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("enc: write after close")
	}
	for _, c := range p {
		w.m.PushByte(c)
		w.data = append(w.data, c)
		w.step()
	}
	w.total += uint64(len(p))
	return len(p), nil
}

func (w *Writer) emitLiteral() {
	w.lit = append(w.lit, w.data[w.front])
	w.front++
}

func (w *Writer) emitMatch() {
	if len(w.lit) > 0 {
		w.out.WriteByte(wire.TagLiteral)
		w.out = wire.AppendVarint(w.out, uint64(len(w.lit)))
		w.out.Write(w.lit)
		w.lit = w.lit[:0]
	}
	w.out.WriteByte(wire.TagMatch)
	w.out = wire.AppendVarint(w.out, uint64(w.md-1))
	w.out = wire.AppendVarint(w.out, uint64(w.ml-wire.MinMatchLen))
	w.front = w.mStart + w.ml
	w.mStart = -1
	w.md, w.ml = 0, 0
}

// step processes toward the newest byte. At entry front is either a pending
// match start or the next unmatched position; invariants keep every byte
// before front already represented in output records.
func (w *Writer) step() {
	for {
		if w.mStart >= 0 {
			end := w.mStart + w.ml
			if end < len(w.data) {
				w.emitMatch() // mismatch/longer-prefix position now visible
				continue
			}
			return // match runs to current end; wait for more bytes
		}
		if w.front >= len(w.data) {
			return
		}
		next := w.data[w.front:]
		maxLen := wire.MaxMatchLen
		if len(next) < maxLen {
			maxLen = len(next)
		}
		d, l := w.m.Find(next, maxLen)
		if l < wire.MinMatchLen {
			w.emitLiteral()
			continue
		}
		w.mStart, w.md, w.ml = w.front, d, l
		if w.front+l < len(w.data) {
			w.emitMatch() // already capped; commit immediately
			continue
		}
		return // reaches end: possibly extendable
	}
}

// finish forces all pending content out (used by Flush/Close).
func (w *Writer) finish() {
	for w.front < len(w.data) {
		if w.mStart >= 0 {
			w.emitMatch()
			continue
		}
		w.emitLiteral()
	}
	if len(w.lit) > 0 {
		w.out.WriteByte(wire.TagLiteral)
		w.out = wire.AppendVarint(w.out, uint64(len(w.lit)))
		w.out.Write(w.lit)
		w.lit = w.lit[:0]
	}
}

// Flush commits a pending match and emits a flush marker. With no uncommitted
// input it emits no bytes.
func (w *Writer) Flush() error {
	if w.closed {
		return errors.New("enc: flush after close")
	}
	if w.front == len(w.data) && len(w.lit) == 0 && w.mStart < 0 {
		return nil
	}
	w.finish()
	w.out.WriteByte(wire.TagFlush)
	return nil
}

// Close finalizes the stream with the trailer.
func (w *Writer) Close() error {
	if w.closed {
		return errors.New("enc: already closed")
	}
	w.finish()
	w.out.WriteByte(wire.TagEnd)
	w.out = wire.AppendVarint(w.out, w.total)
	w.out = wire.AppendVarint(w.out, uint64(wire.Checksum(w.data)))
	w.closed = true
	return nil
}

// Compress returns the complete compressed stream for data.
func Compress(data []byte, cfg Config) ([]byte, error) {
	w, err := NewWriter(cfg)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return append([]byte(nil), w.Bytes()...), nil
}

type blockJob struct {
	idx    int
	preset []byte
	body   []byte
}

func encodeBlock(cfg Config, preset, body []byte) ([]byte, error) {
	m, err := match.New(cfg.Window, cfg.ChainLimit)
	if err != nil {
		return nil, err
	}
	for _, c := range preset {
		m.PushByte(c)
	}
	var out bytes.Buffer
	var lit []byte
	off := 0
	for off < len(body) {
		maxLen := wire.MaxMatchLen
		if len(body)-off < maxLen {
			maxLen = len(body) - off
		}
		d, l := m.Find(body[off:], maxLen)
		if l < wire.MinMatchLen {
			lit = append(lit, body[off])
			m.PushByte(body[off])
			off++
			continue
		}
		if len(lit) > 0 {
			out.WriteByte(wire.TagLiteral)
			out = wire.AppendVarint(out, uint64(len(lit)))
			out.Write(lit)
			lit = lit[:0]
		}
		out.WriteByte(wire.TagMatch)
		out = wire.AppendVarint(out, uint64(d-1))
		out = wire.AppendVarint(out, uint64(l-wire.MinMatchLen))
		for k := 0; k < l; k++ {
			m.PushByte(body[off+k])
		}
		off += l
	}
	if len(lit) > 0 {
		out.WriteByte(wire.TagLiteral)
		out = wire.AppendVarint(out, uint64(len(lit)))
	out.Write(lit)
	}
	return out.Bytes(), nil
}

// CompressParallel splits data into fixed blocks compressed concurrently;
// block k may use up to cfg.Window trailing bytes of block k-1 as a preset
// dictionary. Output is identical for any positive worker count.
func CompressParallel(data []byte, blockSize, workers int, cfg Config) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, errors.New("enc: blockSize and workers must be > 0")
	}
	n := (len(data) + blockSize - 1) / blockSize
	if n == 0 {
		n = 1
	}
	jobs := make(chan blockJob)
	results := make([][]byte, n)
	done := make(chan struct{})
	var firstErr error
	for i := 0; i < workers; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := range jobs {
				b, err := encodeBlock(cfg, j.preset, j.body)
				if err != nil && firstErr == nil {
					firstErr = err
				}
				results[j.idx] = b
			}
		}()
	}
	for i := 0; i < n; i++ {
		start := i * blockSize
		end := start + blockSize
		if end > len(data) {
			end = len(data)
		}
		pStart := start - cfg.Window
		if pStart < 0 {
			pStart = 0
		}
		jobs <- blockJob{idx: i, preset: data[pStart:start], body: data[start:end]}
	}
	close(jobs)
	for i := 0; i < workers; i++ {
		<-done
}
	if firstErr != nil {
		return nil, firstErr
	}
	var out bytes.Buffer
	out.Write(wire.Magic)
	out = wire.AppendVarint(out, uint64(cfg.Window))
	for i := 0; i < n; i++ {
		out.Write(results[i])
		if i < n-1 {
			out.WriteByte(wire.TagFlush)
		}
	}
	out.WriteByte(wire.TagEnd)
	out = wire.AppendVarint(out, uint64(len(data)))
	out = wire.AppendVarint(out, uint64(wire.Checksum(data)))
	return out.Bytes(), nil
}
