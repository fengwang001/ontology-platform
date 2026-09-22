// Package chunked implements a streaming decoder for chunked
// transfer-encoding: a sequence of "size line + data + CRLF" chunks,
// a zero-size chunk, optional trailer lines, and a final empty line.
//
// Bytes may arrive fragmented at any position; Write may be called
// any number of times and consumed input is never needed again.
// Decoders keep all state in process memory. A single Decoder is NOT
// safe for concurrent use; use one Decoder per stream (independent
// Decoders in separate goroutines are fine).
package chunked

import (
	"ontology/frame"
	"ontology/hexline"
)

// State identifies the decoder's position within a message.
type State int

const (
	// StateSizeLine means a chunk size line is being read.
	StateSizeLine State = iota
	// StateData means chunk data is being read.
	StateData
	// StateAwaitCRLF means the CRLF after chunk data is pending.
	StateAwaitCRLF
	// StateTrailer means trailer lines are being read.
	StateTrailer
	// StateDone means the message is complete.
	StateDone
)

// String returns a short human-readable state name.
func (s State) String() string {
	switch s {
	case StateSizeLine:
		return "size-line"
	case StateData:
		return "chunk-data"
	case StateAwaitCRLF:
		return "await-crlf"
	case StateTrailer:
		return "trailer"
	case StateDone:
		return "done"
	}
	return "unknown"
}

// Config holds the decoder limits. Non-positive fields are replaced
// by DefaultConfig values.
type Config struct {
	MaxLineLen   int    // max bytes of a size or trailer line, excluding CRLF
	MaxChunkSize uint64 // max bytes in a single chunk
	MaxBodySize  uint64 // max total decoded body bytes
	MaxTrailers  int    // max number of trailer lines
}

// DefaultConfig returns the limits used for non-positive fields.
func DefaultConfig() Config {
	return Config{MaxLineLen: 1024, MaxChunkSize: 1 << 20, MaxBodySize: 16 << 20, MaxTrailers: 64}
}

// Decoder is a streaming chunked transfer-encoding decoder.
type Decoder struct {
	cfg      Config
	frame    *frame.Frame
	trailers *hexline.Scanner
	state    State
	body     []byte
	offset   int
	ntrailer int
	done     bool
	err      error
}

// New returns a Decoder with the given limits.
func New(cfg Config) *Decoder {
	d := DefaultConfig()
	if cfg.MaxLineLen > 0 {
		d.MaxLineLen = cfg.MaxLineLen
	}
	if cfg.MaxChunkSize > 0 {
		d.MaxChunkSize = cfg.MaxChunkSize
	}
	if cfg.MaxBodySize > 0 {
		d.MaxBodySize = cfg.MaxBodySize
	}
	if cfg.MaxTrailers > 0 {
		d.MaxTrailers = cfg.MaxTrailers
	}
	return &Decoder{cfg: d, frame: frame.New(d.MaxLineLen), state: StateSizeLine}
}

// Done reports whether the complete message (including the empty line
// ending the trailer section) has been consumed.
func (d *Decoder) Done() bool {
	return d.done
}

// State reports the decoder's current state.
func (d *Decoder) State() State {
	return d.state
}

// Body returns a copy of the body decoded so far. Before Done it is
// partial; it is never cleared or altered after an error.
func (d *Decoder) Body() []byte {
	return append([]byte(nil), d.body...)
}
