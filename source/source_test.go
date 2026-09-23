package source

import (
	"errors"
	"testing"
)

func TestSource(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		seek    int64
		wantN   int
		wantErr error
		wantEOF int64
	}{
		{"immediate eof", Config{Count: 0}, 0, 0, nil, 0},
		{"three records same key", Config{Count: 3}, 0, 3, nil, 3},
		{"seeded keys", Config{Count: 5, KeySeed: 2}, 0, 5, nil, 5},
		{"injected failure", Config{Count: 10, FailAfter: 4}, 0, 4, ErrInjected, 4},
		{"resume from offset", Config{Count: 6}, 2, 4, nil, 6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.cfg)
			if tc.seek > 0 {
				s.Seek(tc.seek)
			}
			var got int
			var err error
			for {
				_, ok, e := s.Next()
				if e != nil {
					err = e
					break
				}
				if !ok {
					break
				}
				got++
			}
			if got != tc.wantN {
				t.Fatalf("records=%d want %d", got, tc.wantN)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if s.Pos() != tc.wantEOF {
				t.Fatalf("pos=%d want %d", s.Pos(), tc.wantEOF)
			}
		})
	}
}

func TestSourceBadAndBlocks(t *testing.T) {
	s := New(Config{Count: 6, BadEvery: 3})
	var bad, good int
	for {
		r, ok, err := s.Next()
		if err != nil || !ok {
			break
		}
		if string(r.Data) == "garbage-line-without-separator" {
			bad++
		} else {
			good++
		}
	}
	if bad != 2 || good != 4 {
		t.Fatalf("bad=%d good=%d want 2/4", bad, good)
	}
	for range 3 {
		s.NoteBlock()
	}
	if s.Blocks() != 3 {
		t.Fatalf("blocks=%d want 3", s.Blocks())
	}
}
