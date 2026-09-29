package split_test

import (
	"errors"
	"testing"

	"ontology/split"
)

type rec struct {
	start, end int
	data       []byte
}

func run(t *testing.T, c split.Config, data []byte, cuts [][]int) []rec {
	t.Helper()
	var got []rec
	c.OnCommit = func(start, end int, b []byte) error {
		got = append(got, rec{start, end, append([]byte(nil), b...)})
		return nil
	}
	s, err := split.New(c)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, step := range cuts {
		for _, n := range step {
			if err := s.Write(data[:n]); err != nil {
				t.Fatalf("Write: %v", err)
			}
			data = data[n:]
		}
	}
	if _, err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return got
}

func TestSplit(t *testing.T) {
	payload := make([]byte, 5000)
	for i := range payload {
		payload[i] = byte(i*131 + i>>5)
	}
	cfg := split.Config{Window: 8, Min: 64, Max: 4096, Mask: 0x7}

	t.Run("write_pattern_independent", func(t *testing.T) {
		patterns := [][]int{
			{len(payload)},
			{1, 1, len(payload) - 2},
			nil,
		}
		// pattern: one byte at a time
		for i := 0; i < len(payload); i++ {
			patterns[2] = append(patterns[2], 1)
		}
		var ref []rec
		for pi, pat := range patterns {
			got := run(t, cfg, append([]byte(nil), payload...), [][]int{pat})
			if pi == 0 {
				ref = got
				continue
			}
			if len(got) != len(ref) {
				t.Fatalf("pattern %d: %d chunks want %d", pi, len(got), len(ref))
			}
			for i := range ref {
				if got[i].start != ref[i].start || got[i].end != ref[i].end ||
					string(got[i].data) != string(ref[i].data) {
					t.Fatalf("pattern %d chunk %d mismatch", pi, i)
				}
			}
		}
	})

	t.Run("bounds_contiguous", func(t *testing.T) {
		got := run(t, cfg, payload, [][]int{{len(payload)}})
		pos := 0
		for i, c := range got {
			if c.start != pos || c.end <= c.start {
				t.Fatalf("chunk %d offsets %d-%d", i, c.start, c.end)
			}
			if i < len(got)-1 && (c.end-c.start < cfg.Min || c.end-c.start > cfg.Max) {
				t.Fatalf("chunk %d len %d out of [%d,%d]", i, c.end-c.start, cfg.Min, cfg.Max)
			}
			if len(c.data) != c.end-c.start {
				t.Fatalf("chunk %d data len mismatch", i)
			}
			pos = c.end
		}
	if pos != len(payload) {
			t.Fatalf("last end %d want %d", pos, len(payload))
		}
	})

	t.Run("forced_max", func(t *testing.T) {
		zero := make([]byte, 500) // constant bytes never satisfy a real hit pattern issues
		c := split.Config{Window: 4, Min: 8, Max: 64, Mask: 0xffff}
		got := run(t, c, zero, [][]int{{len(zero)}})
		for i, ch := range got {
			if i < len(got)-1 && ch.end-ch.start != 64 {
				t.Fatalf("chunk %d len %d want forced 64", i, ch.end-ch.start)
			}
		}
	})

	t.Run("invalid_params_distinct", func(t *testing.T) {
		for _, tc := range []struct {
			cfg split.Config
			err error
		}{
			{split.Config{Window: 4, Min: 100, Max: 50}, split.ErrBadBound},
			{split.Config{Window: 0, Min: 10, Max: 50}, split.ErrBadWindow},
			{split.Config{Window: 20, Min: 10, Max: 50}, split.ErrBadWindow},
		} {
			if _, err := split.New(tc.cfg); !errors.Is(err, tc.err) {
				t.Fatalf("cfg=%+v err=%v want %v", tc.cfg, err, tc.err)
			}
		}
	})

	t.Run("rejected_commit_leaves_no_trace", func(t *testing.T) {
		boom := errors.New("boom")
		c := cfg
		emitted := 0
		c.OnCommit = func(start, end int, b []byte) error {
			emitted++
			if emitted == 2 {
				return boom
			}
			return nil
		}
		s, _ := split.New(c)
		_ = s.Write(payload[:200])
		first := emitted
		if err := s.Write(payload[200:]); !errors.Is(err, boom) {
			t.Fatalf("err=%v want boom", err)
		}
		_ = s.Flush()
		if emitted <= first {
			t.Fatalf("splitter unusable after rejection: emitted=%d first=%d", emitted, first)
		}
	})
}
