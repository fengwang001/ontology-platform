package sig

import (
	"bytes"
	"testing"

	"ontology/chunk"
	"ontology/verify"
)

// TestEncodeDecodeRoundTrip 对多种数据形态做签名往返并校验内容。
func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []struct {
		name      string
		data      []byte
		n         uint32
		wantEntry int
	}{
		{"empty", nil, 4, 0},
		{"exact", []byte{1, 2, 3, 4}, 2, 2},
		{"short-tail", []byte{1, 2, 3, 4, 5}, 2, 3},
		{"block-bigger", []byte{9, 9}, 8, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig, err := Generate(tc.data, tc.n)
			if err != nil {
				t.Fatal(err)
			}
			raw := sig.Encode()
			got, err := Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			if got.BlockSize != tc.n || len(got.Entries) != tc.wantEntry {
				t.Fatalf("decoded n=%d entries=%d", got.BlockSize, len(got.Entries))
			}
			if !bytes.Equal(raw, got.Encode()) {
				t.Fatalf("re-encode not byte-identical")
			}
			blocks, _ := chunk.Split(tc.data, tc.n)
			for i, b := range blocks {
				if got.Entries[i].Weak != b.Weak || got.Entries[i].Strong != b.Strong {
					t.Fatalf("entry %d mismatch", i)
				}
			}
		})
	}
}

// TestDecodeErrors 对畸形/截断签名断言可判定错误。
func TestDecodeErrors(t *testing.T) {
	good := func() []byte {
		s, _ := Generate([]byte{1, 2, 3, 4, 5}, 2)
		return s.Encode()
	}()
	cases := []struct {
		name string
		raw  []byte
		want error
	}{
		{"too-short", good[:7], verify.ErrSignature},
		{"bad-magic", func() []byte { b := append([]byte{}, good...); b[0] = 'X'; return b }(), verify.ErrSignature},
		{"bad-tail-length", append(good, 1, 2, 3), verify.ErrSignature},
		{"zero-block", []byte{'O', 'S', 'I', 'G', 0, 0, 0, 0}, verify.ErrBadBlockSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode(tc.raw); err != tc.want {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}

// TestLookupGroups 弱和索引能找回对应块。
func TestLookupGroups(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	sig, _ := Generate(data, 4)
	m := sig.Lookup()
	if len(m) != 2 {
		t.Fatalf("groups=%d want 2", len(m))
	}
	blocks, _ := chunk.Split(data, 4)
	for i := range blocks {
		if es := m[blocks[i].Weak]; len(es) != 1 || es[0].Index != uint32(i) {
			t.Fatalf("lookup mismatch for block %d", i)
		}
	}
}
