package vv

import (
	"errors"
	"testing"
)

func TestCodecRoundTripTable(t *testing.T) {
	entry := Entry{Key: "k", Value: "v", Origin: "A", Version: Vector{"A": 2, "B": 1}}
	cases := []struct {
		name string
		data []byte
	}{
		{"vector", EncodeVector(entry.Version)},
		{"entry", EncodeEntry(entry)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "vector" {
				got, err := DecodeVector(tc.data)
				if err != nil || Compare(got, entry.Version) != Equal {
					t.Fatalf("vector round trip: %#v %v", got, err)
				}
			} else {
				got, err := DecodeEntry(tc.data)
				if err != nil || got.Key != entry.Key || got.Value != entry.Value ||
					got.Origin != entry.Origin || Compare(got.Version, entry.Version) != Equal {
					t.Fatalf("entry round trip: %#v %v", got, err)
				}
			}
		})
	}
}

func TestEveryTruncationIsClassified(t *testing.T) {
	data := EncodeEntry(Entry{Key: "kk", Value: "vv", Origin: "A", Version: Vector{"A": 7}})
	seen := map[error]bool{}
	known := []error{ErrTruncatedHeader, ErrTruncatedComponent, ErrCRCMismatch}
	for cut := 1; cut < len(data); cut++ {
		_, err := DecodeEntry(data[:cut])
		matched := false
		for _, want := range known {
			if errors.Is(err, want) {
				matched, seen[want] = true, true
			}
		}
		if !matched {
			t.Fatalf("cut %d returned unclassified error %v", cut, err)
		}
	}
	for _, want := range known {
		if !seen[want] {
			t.Fatalf("missing error class %v", want)
		}
	}
}
