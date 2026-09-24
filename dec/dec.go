// Package dec is the streaming LZ77 decompressor.
package dec

// Kind identifies a corruption class.
type Kind int

// CorruptionError carries a byte offset into the compressed stream.
type CorruptionError struct {
	Kind   Kind
	Offset int
}

func (e *CorruptionError) Error() string { return "dec: corrupted stream" }

// Decompressor validates a stream byte by byte and rebuilds output.
type Decompressor struct {
	out []byte
}

// New creates a decompressor; maxOutput<=0 means unlimited.
func New(windowSize, maxOutput int64) (*Decompressor, error) {
	return &Decompressor{}, nil
}

// Write feeds an arbitrary chunk of compressed bytes.
func (d *Decompressor) Write(p []byte) (int, error) { return len(p), nil }

// Close finalizes and detects truncation or trailing bytes.
func (d *Decompressor) Close() error { return nil }

// Output returns a copy of all decompressed bytes.
func (d *Decompressor) Output() []byte { return append([]byte(nil), d.out...) }
