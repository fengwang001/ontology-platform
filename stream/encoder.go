package stream

import "ontology/b64"

// EncodeConfig configures a streaming encoder.
type EncodeConfig struct {
	MIME bool // insert \r\n every 76 characters, never after the final line
}

// Encoder incrementally encodes bytes into strict canonical Base64.
type Encoder struct {
	mime bool
	buf  [3]byte
	n    int
	out  []byte
	col  int // characters on the current output line
}

// NewEncoder creates an encoder from cfg.
func NewEncoder(cfg EncodeConfig) *Encoder {
	return &Encoder{mime: cfg.MIME}
}

// Output returns the encoded characters produced so far (final on Close).
func (e *Encoder) Output() []byte { return e.out }

func (e *Encoder) emit(g [4]byte) {
	if e.mime && e.col == 76 {
		e.out = append(e.out, '\r', '\n')
		e.col = 0
	}
	e.out = append(e.out, g[:]...)
	e.col += 4
}

// Write feeds raw bytes to encode.
func (e *Encoder) Write(p []byte) (int, error) {
	for _, c := range p {
		e.buf[e.n] = c
		e.n++
		if e.n == 3 {
			e.emit(b64.EncodeGroup(e.buf[:3]))
			e.n = 0
		}
	}
	return len(p), nil
}

// Close flushes the final padded group. No trailing newline is ever emitted.
func (e *Encoder) Close() error {
	if e.n > 0 {
		e.emit(b64.EncodeGroup(e.buf[:e.n]))
		e.n = 0
	}
	return nil
}

// Encode encodes all of src in one pass using cfg.
func Encode(src []byte, cfg EncodeConfig) []byte {
	e := NewEncoder(cfg)
	_, _ = e.Write(src)
	_ = e.Close()
	return e.Output()
}
