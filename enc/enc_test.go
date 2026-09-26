package enc_test

import (
	"bytes"
	"math/rand"
	"testing"

	"ontology/dec"
	"ontology/enc"
)

func roundtrip(t *testing.T, data []byte) []byte {
	t.Helper()
	z, err := enc.Compress(data)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	out, err := dec.Decompress(z, 1<<30)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(out, data) {
		t.Fatalf("roundtrip mismatch len=%d", len(data))
	}
	return z
}

func TestRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	cases := [][]byte{
		nil,
		{},
		bytes.Repeat([]byte{'A'}, 1),
		bytes.Repeat([]byte{'X'}, 100000),
		bytes.Repeat([]byte("abc"), 100),
	}
	rnd := make([]byte, 50000)
	rng.Read(rnd)
	cases = append(cases, rnd)
	for i, c := range cases {
		roundtrip(t, c)
		t.Logf("case %d len=%d", i, len(c))
	}
}

func TestWriteChunkInvariant(t *testing.T) {
	data := bytes.Repeat([]byte("abcdefg"), 5000)
	sizes := []int{1, 7, len(data)}
	var ref []byte
	for _, sz := range sizes {
		e, _ := enc.New()
		for i := 0; i < len(data); i += sz {
			j := i + sz
			if j > len(data) {
				j = len(data)
			}
			e.Write(data[i:j])
		}
		e.Close()
		if ref == nil {
			ref = e.Bytes()
		} else if !bytes.Equal(ref, e.Bytes()) {
			t.Fatalf("chunk size %d differs", sz)
		}
	}
}
