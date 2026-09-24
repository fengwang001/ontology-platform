package patch

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/diff"
	"ontology/sig"
	"ontology/verify"
)

const size = 8

func sample() (src, tgt []byte) {
	tgt = make([]byte, 6*size)
	for i := range tgt {
		tgt[i] = byte(i*3 + 1)
	}
	src = append([]byte(nil), tgt...)
	for j := 0; j < size; j++ {
		src[2*size+j] = byte(0xF0 + j)
	}
	return src, tgt
}

func encodePatch(t *testing.T, src, tgt []byte) []byte {
	t.Helper()
	s, err := sig.Generate(tgt, size)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := diff.Build(src, s)
	return verify.Encode(p, len(tgt))
}

func TestHappyPathAndBoundaries(t *testing.T) {
	src, tgt := sample()
	cases := []struct {
		name string
		src  []byte
		tgt  []byte
	}{
		{"both empty", nil, nil},
		{"src empty", nil, tgt},
		{"tgt empty", src, nil},
		{"block bigger than data", []byte("ab"), []byte("cd")},
		{"exact multiple", src, tgt},
		{"non multiple", append(src, 1, 2, 3), append(tgt, 9, 8, 7, 6)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pb := encodePatch(t, c.src, c.tgt)
			p, err := verify.Decode(pb)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ApplyBytes(p, c.tgt)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, c.src) {
				t.Fatal("rebuilt != source")
			}
		})
	}
}

func TestBadBlockSize(t *testing.T) {
	if _, err := sig.Generate([]byte("abc"), 0); err == nil {
		t.Fatal("block size 0 must be rejected")
	}
}

func TestEveryTruncationPoint(t *testing.T) {
	src, tgt := sample()
	pb := encodePatch(t, src, tgt)
	dir := t.TempDir()

	seen := map[error]int{}
	want := []error{
		verify.ErrHeaderTruncated,
		verify.ErrInstrTruncated,
		verify.ErrDataTruncated,
		verify.ErrCRCMismatch,
	}
	for n := 1; n < len(pb); n++ {
		path := filepath.Join(dir, "target.bin")
		if err := os.WriteFile(path, tgt, 0o644); err != nil {
			t.Fatal(err)
		}
		err := ApplyFile(pb[:n], path)
		if err == nil {
			t.Fatalf("truncation n=%d unexpectedly applied", n)
		}
		matched := false
		for _, w := range want {
			if errors.Is(err, w) {
				seen[w]++
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("n=%d unclassified error: %v", n, err)
		}
		got, _ := os.ReadFile(path)
		if !bytes.Equal(got, tgt) {
			t.Fatalf("n=%d target partially modified", n)
		}
	}
	for _, w := range want {
		if seen[w] == 0 {
			t.Fatalf("error class never observed: %v", w)
		}
	}
}

func TestCorruptFullPatch(t *testing.T) {
	src, tgt := sample()
	pb := encodePatch(t, src, tgt)
	bad := bytes.Clone(pb)
	bad[len(bad)/2] ^= 0xFF
	p, err := verify.Decode(bad)
	if err == nil {
		if _, err := ApplyBytes(p, tgt); err == nil {
			t.Fatal("corrupt patch must fail")
		}
	}
}

func TestBlockOutOfRange(t *testing.T) {
	src, tgt := sample()
	s, _ := sig.Generate(tgt, size)
	p, _ := diff.Build(src, s)
	for i := range p.Instrs {
		in := &p.Instrs[i]
		if in.Op == diff.OpRef {
			in.Block = 999
		}
	}
	_, err := ApplyBytes(p, tgt)
	if !errors.Is(err, verify.ErrBlockOutOfRange) {
		t.Fatalf("want ErrBlockOutOfRange, got %v", err)
	}
}

func TestSignatureMismatchWithData(t *testing.T) {
	src, tgt := sample()
	s, _ := sig.Generate(tgt, size)
	// 目标在签名生成后、应用前被改动。
	modified := bytes.Clone(tgt)
	modified[0] ^= 0xFF
	p, _ := diff.Build(src, s)
	_, err := ApplyBytes(p, modified)
	if !errors.Is(err, verify.ErrResultMismatch) {
		t.Fatalf("want ErrResultMismatch, got %v", err)
	}
}

func TestApplyFileSuccess(t *testing.T) {
	src, tgt := sample()
	pb := encodePatch(t, src, tgt)
	path := filepath.Join(t.TempDir(), "t.bin")
	if err := os.WriteFile(path, tgt, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyFile(pb, path); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, src) {
		t.Fatal("file content != source")
	}
}
