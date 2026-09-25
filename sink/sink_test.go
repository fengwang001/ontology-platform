package sink

import (
	"bytes"
	"errors"
	"testing"
)

func TestScripted(t *testing.T) {
	cases := []struct {
		name      string
		steps     []func(s *Scripted)
		write     string
		wantN     int
		wantErr   error
		wantBytes string
	}{
		{
			"short-write",
			[]func(s *Scripted){func(s *Scripted) { s.MaxAccept = 3; s.Quota = 100 }},
			"abcdef", 3, nil, "abc",
		},
		{
			"backpressure",
			nil,
			"abc", 0, ErrBackpressure, "",
		},
		{
			"disconnect",
			[]func(s *Scripted){func(s *Scripted) { s.Quota = 100; s.CutAfter = 2 }},
			"abcdef", 2, ErrDisconnect, "ab",
		},
		{
			"injected-error",
			[]func(s *Scripted){func(s *Scripted) { s.MaxAccept = 1; s.Quota = 100; s.FailAt = 1; s.FailErr = errors.New("boom") }},
			"abcdef", 1, errors.New("boom"), "a",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Scripted{CutAfter: -1, FailAt: -1}
			for _, st := range tc.steps {
				st(s)
			}
			n, err := s.Write([]byte(tc.write))
			if n != tc.wantN {
				t.Fatalf("n = %d, want %d", n, tc.wantN)
			}
			if (err == nil) != (tc.wantErr == nil) ||
				(tc.wantErr != nil && err != nil && err.Error() != tc.wantErr.Error()) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if string(s.Bytes()) != tc.wantBytes {
				t.Fatalf("bytes = %q, want %q", s.Bytes(), tc.wantBytes)
			}
		})
	}
}

func TestQuotaRefillAndMemory(t *testing.T) {
	s := &Scripted{CutAfter: -1, FailAt: -1}
	if n, err := s.Write([]byte("a")); n != 0 || !errors.Is(err, ErrBackpressure) {
		t.Fatalf("initial write: %d %v", n, err)
	}
	s.AddQuota(3)
	if n, _ := s.Write([]byte("abcdef")); n != 3 {
		t.Fatalf("quota limited n = %d", n)
	}
	if n, err := s.Write([]byte("x")); n != 0 || !errors.Is(err, ErrBackpressure) {
		t.Fatalf("quota exhausted: %d %v", n, err)
	}
	s.AddQuota(3)
	if n, _ := s.Write([]byte("def")); n != 3 {
		t.Fatalf("refill n = %d", n)
	}
	if string(s.Bytes()) != "abcdef" {
		t.Fatalf("bytes = %q", s.Bytes())
	}

	mem := &Memory{}
	if n, err := mem.Write([]byte("xyz")); n != 3 || err != nil {
		t.Fatalf("memory: %d %v", n, err)
	}
	if !bytes.Equal(mem.Bytes(), []byte("xyz")) {
		t.Fatal("memory bytes mismatch")
	}
}
