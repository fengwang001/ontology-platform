// Package api is the single public entry point to the framed codec. It only
// depends on frame; storage, indexing, query parsing, links, actions and HTTP
// are deliberately out of scope.
package api

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/esc"
	"ontology/frame"
)

// API is a stateless codec handle; all its methods are safe for concurrent
// use because every piece of state lives on the call stack.
type API struct{}

// New returns the codec.
func New() *API { return &API{} }

// EncodeFrames encodes frames into one delimited stream.
func (a *API) EncodeFrames(frames [][]byte) []byte { return frame.Encode(frames) }

// DecodeFrames decodes one stream, failing wholesale on any bad frame.
func (a *API) DecodeFrames(data []byte) ([][]byte, error) { return frame.Decode(data) }

// builtinVectors exercises empty frames and both escapable bytes.
func builtinVectors() [][]byte {
	return [][]byte{
		[]byte("hi"),
		[]byte("a\nb"),
		[]byte("x\\y"),
		{},
		[]byte("\n\\"),
	}
}

// naiveEncode is the textbook reference written independently from
// frame.Encode: per byte, '\\' -> "\\\\", '\n' -> "\\n", else copied, one bare
// '\n' after each frame.
func naiveEncode(frames [][]byte) []byte {
	var out bytes.Buffer
	for _, f := range frames {
		for _, b := range f {
			switch b {
			case '\\':
				out.WriteByte('\\')
				out.WriteByte('\\')
			case '\n':
				out.WriteByte('\\')
				out.WriteByte('n')
			default:
				out.WriteByte(b)
			}
		}
		out.WriteByte('\n')
	}
	return out.Bytes()
}

func equalFrames(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// SelfCheck runs all four invariants over the built-in vectors and the three
// distinct failure kinds. It returns nil when everything holds.
func (a *API) SelfCheck() error {
	vec := builtinVectors()
	enc := frame.Encode(vec)

	if !bytes.Equal(enc, naiveEncode(vec)) { // invariant 3
		return errors.New("selfcheck: encode diverges from naive reference")
	}
	dec, err := frame.Decode(enc) // invariants 1 & 2
	if err != nil || !equalFrames(dec, vec) {
		return fmt.Errorf("selfcheck: round trip mismatch: %v", err)
	}

	bad := []struct {
		name string
		raw  []byte
		want error
	}{
		{"illegal escape", []byte("a\\x\n"), esc.ErrIllegalEscape},
		{"dangling escape", []byte("ab\\\n"), esc.ErrDanglingEscape},
		{"missing terminator", []byte("ab"), frame.ErrMissingTerminator},
	}
	for _, c := range bad { // invariant 4: whole failure, distinct sentinels
		got, err := frame.Decode(c.raw)
		if !errors.Is(err, c.want) || got != nil {
			return fmt.Errorf("selfcheck: %s not rejected correctly: %v", c.name, err)
		}
	}

	// Cursor commits only on success; after an error the Reader stays usable.
	r := frame.NewReader(append(append([]byte("ok\n"), []byte("a\\x\n")...), []byte("z\n")...))
	if f, err := r.NextFrame(); err != nil || !bytes.Equal(f, []byte("ok")) {
		return errors.New("selfcheck: reader first frame wrong")
	}
	p := r.Pos()
	if _, err := r.NextFrame(); !errors.Is(err, esc.ErrIllegalEscape) || r.Pos() != p {
		return errors.New("selfcheck: reader advanced cursor after failure")
	}
	if _, err := r.NextFrame(); !errors.Is(err, esc.ErrIllegalEscape) || r.Pos() != p {
		return errors.New("selfcheck: reader unusable after failure")
	}
	return nil
}
