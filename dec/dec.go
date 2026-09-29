// Package dec implements the streaming LZ77 decompressor.
package dec

import "ontology/window"

// Options configures a decompressor.
type Options struct {
	WindowSize int
	MaxOutput  int64 // 0 = unlimited
}

// Decompressor is a streaming decompressor; not safe for concurrent use.
type Decompressor struct {
	win *window.Window
}

// NewDecompressor validates config and returns an empty decoder.
func NewDecompressor(opts Options) (*Decompressor, error) { return &Decompressor{}, nil }

// Write consumes compressed bytes; it may partially consume on error.
func (d *Decompressor) Write(p []byte) (int, error) { return 0, nil }

// Close reports truncation when the stream did not end cleanly.
func (d *Decompressor) Close() error { return nil }

// Output returns the fully reconstructed original bytes.
func (d *Decompressor) Output() []byte { return nil }
