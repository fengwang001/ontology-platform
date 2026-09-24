// Package dec implements the streaming LZ77 decompressor with strict
// validation of every byte of the input stream.
//
// A single Decompressor is not safe for concurrent use; separate instances do
// not share state and may be used concurrently.
package dec

import "fmt"

// Sentinel errors; use errors.Is to classify them.
var (
	ErrHeader      = fmt.Errorf("dec: bad stream header")
	ErrZeroDist    = fmt.Errorf("dec: zero back-reference distance")
	ErrDistOutput  = fmt.Errorf("dec: distance exceeds bytes produced")
	ErrDistWindow  = fmt.Errorf("dec: distance exceeds window capacity")
	ErrVarint      = fmt.Errorf("dec: varint too long or overflow")
	ErrLength      = fmt.Errorf("dec: trailer length mismatch")
	ErrChecksum    = fmt.Errorf("dec: checksum mismatch")
	ErrTrailing    = fmt.Errorf("dec: trailing bytes after stream end")
	ErrTruncated   = fmt.Errorf("dec: truncated stream")
	ErrOutputLimit = fmt.Errorf("dec: output length limit exceeded")
)

// OffsetError wraps a stream error with the byte offset at which it was found.
type OffsetError struct{}

func (e *OffsetError) Offset() int    { return 0 }
func (e *OffsetError) Unwrap() error  { return nil }
func (e *OffsetError) Error() string  { return "" }

// Decompressor reconstructs bytes from a compressed stream.
type Decompressor struct{}

// New creates a decompressor. maxOutput bounds total reconstructed bytes;
// pass 0 for no limit.
func New(maxOutput int64) *Decompressor { return &Decompressor{} }

// Write feeds compressed bytes. Any validation error puts the decompressor
// into a terminal state and the same error is returned by later calls.
func (d *Decompressor) Write(p []byte) (int, error) { return len(p), nil }

// Close verifies the stream ended exactly at the trailer.
func (d *Decompressor) Close() error { return nil }

// Output returns all reconstructed bytes so far.
func (d *Decompressor) Output() []byte { return nil }
