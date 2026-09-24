// Package qp implements a Quoted-Printable (RFC 2045) encoder and a strict
// streaming decoder. It depends only on the standard library and on qpline.
package qp

// Encode returns the Quoted-Printable encoding of src.
func Encode(src []byte) []byte { return nil }

// EncodeExamined is Encode plus the total number of input bytes inspected.
func EncodeExamined(src []byte) ([]byte, int) { return nil, 0 }

// Decode decodes src in one shot and returns the decoded bytes.
func Decode(src []byte) ([]byte, error) { return nil, nil }

// Decoder is a strict streaming Quoted-Printable decoder.
type Decoder struct {
	out []byte
}

// NewDecoder returns an empty strict decoder.
func NewDecoder() *Decoder { return &Decoder{} }

// Write feeds a chunk of encoded input.
func (d *Decoder) Write(p []byte) (int, error) { return len(p), nil }

// Close must be called after the final Write to validate a dangling escape.
func (d *Decoder) Close() error { return nil }

// Output returns all decoded bytes produced so far.
func (d *Decoder) Output() []byte { return d.out }
