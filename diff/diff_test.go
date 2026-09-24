package diff_test

import (
	"bytes"
	"reflect"
	"sync"
	"testing"

	"ontology/chunk"
	"ontology/diff"
	"ontology/patch"
	"ontology/sig"
)

func compute(t *testing.T, src, tgt []byte, bs int) (patch.Patch, diff.Stats) {
	t.Helper()
	sg, err := sig.Build(tgt, bs)
	if err != nil {
		t.Fatalf("sig.Build: %v", err)
	}
	p, st, err := diff.Compute(src, sg)
	if err != nil {
		t.Fatalf("diff.Compute: %v", err)
	}
	out, err := patch.Apply(tgt, patch.Encode(p))
	if err != nil || !bytes.Equal(out, src) {
		t.Fatalf("apply result mismatch: err=%v", err)
	}
	return p, st
}

// blocksOf 生成 n 个 bs 字节块，第 i 块全部填充字节 i+1。
func blocksOf(n, bs int) []byte {
	b := make([]byte, n*bs)
	for i := range b {
		b[i] = byte(i/bs + 1)
	}
	return b
}

func TestDiff(t *testing.T) {
	eight := blocksOf(8, 8) // 64B，8 块
	ten := blocksOf(10, 8)  // 80B，10 块
	mid := append([]byte(nil), eight...)
	for i := 32; i < 40; i++ {
		mid[i] = 0x55 // 替换中间第 4 块
	}

	t.Run("relationships patch composition", func(t *testing.T) {
		cases := []struct {
			name               string
			src, tgt           []byte
			bs                 int
			wantNew, wantReuse int
		}{
			{"identical", eight, eight, 8, 0, 8},
			{"mid block differs", mid, eight, 8, 8, 7},
			{"target is prefix", ten, ten[:48], 8, 32, 6},
			{"source is prefix", ten[:48], ten, 8, 0, 6},
			{"disjoint", bytes.Repeat([]byte{0xAA}, 40), bytes.Repeat([]byte{0xBB}, 40), 8, 40, 0},
			{"empty source", nil, eight, 8, 0, 0},
			{"empty target", eight, nil, 8, 64, 0},
			{"both empty", nil, nil, 8, 0, 0},
		}
		for _, tc := range cases {
			_, st := compute(t, tc.src, tc.tgt, tc.bs)
			if st.NewBytes != tc.wantNew || st.ReusedBlocks != tc.wantReuse {
				t.Errorf("%s: new=%d reuse=%d want new=%d reuse=%d",
					tc.name, st.NewBytes, st.ReusedBlocks, tc.wantNew, tc.wantReuse)
			}
		}
	})

	t.Run("weak collision blocked by strong", func(t *testing.T) {
		tgt := []byte{1, 2, 3, 4}
		src := []byte{4, 3, 2, 1}
		if chunk.WeakSum(tgt) != chunk.WeakSum(src) {
			t.Fatal("test requires colliding weak sums")
		}
		_, st := compute(t, src, tgt, 4)
		if st.ReusedBlocks != 0 || st.NewBytes != 4 {
			t.Errorf("collision caused reuse: %+v", st)
		}
	})

	t.Run("strong checks bounded by weak matches", func(t *testing.T) {
		_, st := compute(t, mid, eight, 8)
		if st.StrongChecks > st.WeakMatches || st.StrongChecks == 0 {
			t.Errorf("strong=%d weak=%d", st.StrongChecks, st.WeakMatches)
		}
	})

	t.Run("deterministic across 20 runs", func(t *testing.T) {
		sg, err := sig.Build(ten, 8)
		if err != nil {
			t.Fatal(err)
		}
		var first []byte
		for run := 0; run < 20; run++ {
			p, _, err := diff.Compute(mid, sg)
			if err != nil {
				t.Fatal(err)
			}
			got := patch.Encode(p)
			if run == 0 {
				first = got
			} else if !bytes.Equal(first, got) {
				t.Fatalf("run %d differs", run)
			}
		}
	})

	t.Run("concurrent diffs do not interfere", func(t *testing.T) {
		var wg sync.WaitGroup
		for g := 0; g < 16; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				tgt := bytes.Repeat([]byte{byte(g + 1)}, 100)
				src := append([]byte(nil), tgt...)
				src[50] ^= 0xFF
				sg, err := sig.Build(tgt, 8)
				if err != nil {
					t.Error(err)
					return
				}
				p, _, err := diff.Compute(src, sg)
				if err != nil {
					t.Error(err)
					return
				}
				out, err := patch.Apply(tgt, patch.Encode(p))
				if err != nil || !bytes.Equal(out, src) {
					t.Errorf("goroutine %d: mismatch err=%v", g, err)
				}
			}(g)
		}
		wg.Wait()
	})

	t.Run("signature encode decode roundtrip", func(t *testing.T) {
		sg, err := sig.Build(ten, 8)
		if err != nil {
			t.Fatal(err)
		}
		back, err := sig.Decode(sg.Encode())
		if err != nil || !reflect.DeepEqual(sg, back) {
			t.Errorf("roundtrip failed: %v", err)
		}
	})
}
