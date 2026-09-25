// Package mtfseq applies the move-to-front transform to whole byte
// sequences. It depends only on mtf.
package mtfseq

import "ontology/mtf"

// Encode transforms data under alphabet alpha into a sequence of 0-based
// indices (one byte each). It returns ErrInvalidAlphabet when alpha is
// empty or has duplicates, and ErrUnknownSymbol on the first symbol not in
// alpha. On failure it returns nil (no partial result): a fresh, validated
// list is used for the call, so a rejected call leaves nothing behind and
// the caller can immediately Encode again.
func Encode(data, alpha []byte) ([]byte, error) {
	c, err := mtf.New(alpha)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(data))
	for _, s := range data {
		i, err := c.EncodeSymbol(s)
		if err != nil {
			return nil, err
		}
		out = append(out, byte(i))
	}
	return out, nil
}

// Decode transforms an index sequence back into symbols under alphabet
// alpha. It returns ErrInvalidAlphabet when alpha is invalid and
// ErrInvalidIndex on the first index i >= len(alpha). On failure it
// returns nil (no partial result) and a fresh list is used per call, so a
// rejected call leaves nothing behind.
func Decode(indices, alpha []byte) ([]byte, error) {
	c, err := mtf.New(alpha)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(indices))
	for _, i := range indices {
		s, err := c.DecodeSymbol(int(i))
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
