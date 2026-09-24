package source

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestGenerator(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		gen      func() *Generator
		wantN    int
		wantOf   []int64
		wantSubs []string
		err      error
	}{
		{
			name:  "zero",
			gen:   func() *Generator { return NewGenerator(0, 0) },
			wantN: 0,
			err:   io.EOF,
		},
		{
			name:     "three defaults",
			gen:      func() *Generator { return NewGenerator(3, 0) },
			wantN:    3,
			wantOf:   []int64{0, 1, 2},
			wantSubs: []string{"a=0", "b=1", "c=2"},
			err:      io.EOF,
		},
		{
			name: "custom",
			gen: func() *Generator {
				g := NewGenerator(2, 0)
				g.Key = func(i int64) string { return "k" }
				g.Val = func(i int64) int { return 42 }
				return g
			},
			wantN:    2,
			wantSubs: []string{"k=42", "k=42"},
			err:      io.EOF,
		},
		{
			name: "fail after one",
			gen: func() *Generator {
				g := NewGenerator(5, 0)
				g.FailAt = 1
				return g
			},
			wantN: 1,
			err:   ErrFailed,
		},
		{
			name: "start offset",
			gen: func() *Generator {
				g := NewGenerator(2, 0)
				g.StartAt = 10
				return g
			},
			wantN:    2,
			wantOf:   []int64{10, 11},
			wantSubs: []string{"c=10", "c=11"},
			err:      io.EOF,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]Raw, 0)
			var last error
			for {
				r, err := tc.gen().Next(ctx)
				if err != nil {
					last = err
					break
				}
				got = append(got, r)
			}
			if len(got) != tc.wantN {
				t.Fatalf("count = %d, want %d", len(got), tc.wantN)
			}
			if last != tc.err {
				t.Fatalf("err = %v, want %v", last, tc.err)
			}
			for i, sub := range tc.wantSubs {
				if !strings.Contains(string(got[i].Data), sub) {
					t.Fatalf("record %d = %q, want contain %q", i, got[i].Data, sub)
				}
			}
			for i, off := range tc.wantOf {
				if got[i].Offset != off {
					t.Fatalf("offset %d = %d, want %d", i, got[i].Offset, off)
				}
			}
		})
	}

	t.Run("rate blocks until cancel", func(t *testing.T) {
		g := NewGenerator(10, 10)
		cctx, cancel := context.WithTimeout(ctx, 25*time.Millisecond)
		defer cancel()
		n := 0
		for {
			if _, err := g.Next(cctx); err != nil {
				break
			}
			n++
		}
		if n >= 10 {
			t.Fatalf("rate not applied: produced %d in 25ms", n)
		}
	})
}
