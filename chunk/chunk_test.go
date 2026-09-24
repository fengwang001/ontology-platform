package chunk

import (
	"errors"
	"math/rand"
	"testing"
)

func TestChunk(t *testing.T) {
	t.Run("split boundaries", func(t *testing.T) {
		cases := []struct {
			name       string
			data       []byte
			size       int
			wantBlocks int
			wantLast   int
			wantErr    error
		}{
			{"empty data", nil, 4, 0, 0, nil},
			{"exact multiple", []byte("aaaabbbb"), 4, 2, 4, nil},
			{"non multiple", []byte("aaaabbbbc"), 4, 3, 1, nil},
			{"block bigger than data", []byte("ab"), 8, 1, 2, nil},
			{"single byte", []byte("a"), 1, 1, 1, nil},
			{"zero block size", []byte("ab"), 0, 0, 0, ErrBlockSize},
			{"negative block size", []byte("ab"), -3, 0, 0, ErrBlockSize},
		}
		for _, tc := range cases {
			blocks, err := Split(tc.data, tc.size)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("%s: err=%v want %v", tc.name, err, tc.wantErr)
				continue
			}
			if tc.wantErr != nil {
				continue
			}
			if len(blocks) != tc.wantBlocks {
				t.Errorf("%s: got %d blocks want %d", tc.name, len(blocks), tc.wantBlocks)
			}
			off := 0
			for i, b := range blocks {
				if b.Index != i || b.Offset != off {
					t.Errorf("%s: block %d has index=%d offset=%d", tc.name, i, b.Index, b.Offset)
				}
				if b.Weak != WeakSum(b.Data) || b.Strong != Strong(b.Data) {
					t.Errorf("%s: block %d checksums wrong", tc.name, i)
				}
				off += len(b.Data)
			}
			if tc.wantBlocks > 0 && len(blocks[len(blocks)-1].Data) != tc.wantLast {
				t.Errorf("%s: last block len=%d want %d", tc.name, len(blocks[len(blocks)-1].Data), tc.wantLast)
			}
		}
	})

	rng := rand.New(rand.NewSource(7))
	data := make([]byte, 300)
	rng.Read(data)
	const bs = 16

	t.Run("rolling equals recompute at every offset", func(t *testing.T) {
		r := NewRoller(data[:bs])
		for i := 0; i+bs <= len(data); i++ {
			if got, want := r.Sum(), WeakSum(data[i:i+bs]); got != want {
				t.Fatalf("offset %d: rolling=%d recompute=%d", i, got, want)
			}
			if i+bs < len(data) {
				r.Roll(data[i], data[i+bs])
			}
		}
	})

	t.Run("weak ops bounded by 4x data length", func(t *testing.T) {
		ResetWeakOps()
		r := NewRoller(data[:bs])
		for i := 0; i+bs < len(data); i++ {
			r.Roll(data[i], data[i+bs])
		}
		if got, limit := WeakOps(), 4*uint64(len(data)); got > limit {
			t.Errorf("weak ops=%d exceeds 4*len=%d", got, limit)
		}
	})

	t.Run("weak collision pair differs under strong", func(t *testing.T) {
		a := []byte{1, 2, 3, 4}
		b := []byte{4, 3, 2, 1}
		if WeakSum(a) != WeakSum(b) {
			t.Fatal("test requires equal weak sums")
		}
		if Strong(a) == Strong(b) {
			t.Fatal("test requires different strong sums")
		}
	})
}
