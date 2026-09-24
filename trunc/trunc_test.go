package trunc

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/log"
)

func buildLog(last uint64) *log.Log {
	lg := log.New()
	for i := uint64(0); i <= last; i++ {
		if _, err := lg.Append("x"); err != nil {
			panic(err)
		}
	}
	return lg
}

// TestCheckpointReadCount: a legal Truncate reads one unexported field, a
// constant independent of m; read white-box here, never via an exported API.
func TestCheckpointReadCount(t *testing.T) {
	const bound = 2
	var first int = -1
	for _, m := range []uint64{100, 1000, 10000} {
		lg := buildLog(m)
		if err := lg.Checkpoint(m); err != nil {
			t.Fatalf("m=%d cp: %v", m, err)
		}
		tr := New(lg)
		k := m / 2
		if err := tr.Truncate(k); err != nil {
			t.Fatalf("m=%d trunc(%d): %v", m, k, err)
		}
		if tr.metaReads > bound {
			t.Fatalf("m=%d: read %d fields, want <= %d", m, tr.metaReads, bound)
		}
		if first < 0 {
			first = tr.metaReads
		} else if tr.metaReads != first {
			t.Fatalf("m=%d: reads %d differ from m=100 value %d", m, tr.metaReads, first)
		}
	}
}

// TestTruncateRejectsUnpersisted: K>cp (or no checkpoint) is refused with the
// distinct sentinel, changing neither marker nor start.
func TestTruncateRejectsUnpersisted(t *testing.T) {
	cases := []struct {
		name   string
		seedCP bool
		k      uint64
	}{
		{"no checkpoint", false, 1},
		{"K one past cp", true, 3},
		{"K far past cp", true, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lg := buildLog(3) // offsets 0..3
			if tc.seedCP {
				_ = lg.Checkpoint(2)
			}
			tr := New(lg)
			if err := tr.Truncate(tc.k); !errors.Is(err, ErrTruncateBeyondCheckpoint) {
				t.Fatalf("Truncate(%d): got %v", tc.k, err)
			}
			if lg.First() != 0 {
				t.Fatalf("rejection moved f to %d", lg.First())
			}
			if _, ok := tr.Marker(); ok {
				t.Fatal("rejection wrote a marker")
			}
		})
	}
}

// TestRecoveryTable: f==tm clean, f<tm converges, f>tm is corruption.
func TestRecoveryTable(t *testing.T) {
	cases := []struct {
		name    string
		marker  uint64
		first   uint64
		wantErr error
		wantF   uint64
	}{
		{"clean f==tm", 3, 3, nil, 3},
		{"interrupted f<tm", 3, 2, nil, 3},
		{"interrupted from zero", 2, 0, nil, 2},
		{"over-delete f>tm", 3, 4, ErrRecoverOverDeletion, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lg := buildLog(4) // cp=4 so every prefix drop is legal
			_ = lg.Checkpoint(4)
			tr := New(lg)
			if tc.first > 0 {
				if err := tr.Truncate(tc.first); err != nil {
					t.Fatalf("seed f=%d: %v", tc.first, err)
				}
			}
			err := tr.Recover(tc.marker, tc.first)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Recover(%d,%d): %v want %v", tc.marker, tc.first, err, tc.wantErr)
			}
			if lg.First() != tc.wantF {
				t.Fatalf("f=%d want %d", lg.First(), tc.wantF)
			}
		})
	}
}

// TestRandomCrashPoints injects a marker-then-crash at a random K and asserts
// recovery always converges f==tm.
func TestRandomCrashPoints(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 100; iter++ {
		n := uint64(5 + r.Intn(20))
		lg := buildLog(n - 1)
		_ = lg.Checkpoint(n - 1)
		tr := New(lg)
		pre := uint64(r.Intn(int(n)))
		if pre > 0 {
			_ = tr.Truncate(pre)
		}
		crash := pre + 1 + uint64(r.Intn(int(n-pre)))
		if crash > n-1 {
			crash = n - 1
		}
		if err := tr.markOnly(crash); err != nil {
			t.Fatalf("iter %d markOnly(%d): %v", iter, crash, err)
		}
		if f := lg.First(); f != pre {
			t.Fatalf("iter %d: crash moved f to %d, want %d", iter, f, pre)
		}
		if err := tr.Recover(crash, lg.First()); err != nil {
			t.Fatalf("iter %d recover: %v", iter, err)
		}
		if lg.First() != crash {
			t.Fatalf("iter %d: f=%d want %d", iter, lg.First(), crash)
		}
		if tm, _ := tr.Marker(); tm != crash {
			t.Fatalf("iter %d: tm=%d want %d", iter, tm, crash)
		}
	}
}
