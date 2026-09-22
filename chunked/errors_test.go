package chunked

import (
	"testing"
)

func TestSixErrorKindsAndOffsets(t *testing.T) {
	cases := []struct {
		name   string
		wire   string
		limits Limits
		kind   Kind
		off    int
	}{
		{
			name: "non-hex size",
			wire: "xg\r\n",
			kind: KindNonHex,
			off:  0,
		},
		{
			name:   "size line too long",
			wire:   "000a\r\n",
			limits: Limits{MaxSizeLine: 4},
			kind:   KindLineTooLong,
			off:    4,
		},
		{
			name: "missing CRLF after data",
			wire: "4\r\nWikiX",
			kind: KindMissingCRLF,
			off:  7,
		},
		{
			name: "half CRLF at end",
			wire: "4\r\nWiki\r",
			kind: KindHalfCRLF,
			off:  8,
		},
		{
			name: "unterminated quote",
			wire: "4;x=\"abc",
			kind: KindUnterminatedQuote,
			off:  8,
		},
		{
			name:   "too many trailers",
			wire:   "0\r\nA: 1\r\nB: 2\r\n",
			limits: Limits{MaxTrailers: 1},
			kind:   KindTooManyTrailers,
			off:    9,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kinds := map[Kind]bool{}
			for size := 1; size <= len(tc.wire); size++ {
				d := New()
				if tc.limits != (Limits{}) {
					d = NewWithLimits(tc.limits)
				}
				var err error
				for off := 0; off < len(tc.wire); {
					end := off + size
					if end > len(tc.wire) {
						end = len(tc.wire)
					}
					var n int
					n, err = d.Write([]byte(tc.wire)[off:end])
					off += n
					if err != nil {
						break
					}
				}
				if err == nil {
					err = d.Close()
				}
				ce, ok := AsError(err)
				if !ok {
					t.Fatalf("chunk %d: not a *chunked.Error: %v", size, err)
				}
				kinds[ce.Kind] = true
				if ce.Kind != tc.kind {
					t.Fatalf("chunk %d: kind %d want %d (%v)", size, ce.Kind, tc.kind, err)
				}
				if ce.Offset != tc.off {
					t.Fatalf("chunk %d: offset %d want %d", size, ce.Offset, tc.off)
				}
			}
			if len(kinds) != 1 {
				t.Fatalf("error kind varied across splits: %v", kinds)
			}
		})
	}
}

func TestKindsAreDistinct(t *testing.T) {
	kinds := []Kind{
		KindNonHex, KindLineTooLong, KindChunkTooLarge, KindBodyTooLarge,
		KindMissingCRLF, KindHalfCRLF, KindUnterminatedQuote,
		KindTooManyTrailers, KindBadTrailer, KindAlreadyDone,
		KindIncompleteHeader, KindIncompleteData, KindIncompleteCRLF,
		KindIncompleteTrailers,
	}
	seen := map[Kind]bool{}
	for _, k := range kinds {
		if seen[k] {
			t.Fatalf("duplicate kind %d", k)
		}
		seen[k] = true
	}
}
