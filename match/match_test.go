package match_test

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"ontology/enc"
	"ontology/match"
	"ontology/window"
)

func compress(t *testing.T, data []byte) ([]byte, int64) {
	t.Helper()
	var buf bytes.Buffer
	w, err := enc.NewWriter(&buf, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), w.Examined()
}

func TestExaminedBound(t *testing.T) {
	cfg := enc.DefaultConfig()
	perByte := map[int]float64{}
	for _, size := range []int{64 * 1024, 4 * 1024 * 1024} {
		for _, kind := range []string{"same", "random"} {
			data := make([]byte, size)
			if kind == "random" {
				r := rand.New(rand.NewPCG(5, 6))
				for i := range data {
					data[i] = byte(r.Uint32())
				}
			} else {
				for i := range data {
					data[i] = 'x'
				}
			}
			stream, examined := compress(t, data)
			if bound := int64(cfg.MaxChain) * int64(size); examined > bound {
				t.Fatalf("%s/%d: examined %d > chain*bytes %d", kind, size, examined, bound)
			}
			if kind == "same" {
				perByte[size] = float64(examined) / float64(size)
				if size == 4*1024*1024 && len(stream) >= 64*1024 {
					t.Fatalf("4MB same-byte compressed to %d >= 64KB", len(stream))
				}
				t.Logf("%s %dB: examined=%d per-byte=%.4f compressed=%d",
					kind, size, examined, perByte[size], len(stream))
			} else {
				t.Logf("%s %dB: examined=%d per-byte=%.4f compressed=%d",
					kind, size, examined, float64(examined)/float64(size), len(stream))
			}
		}
	}
	lo, hi := perByte[64*1024], perByte[4*1024*1024]
	if hi > 2*lo || lo > 2*hi {
		t.Fatalf("per-byte examined diverges with scale: %g vs %g", lo, hi)
	}
}

func TestBadConfig(t *testing.T) {
	var buf bytes.Buffer
	if _, err := window.New(0); err == nil {
		t.Fatal("window.New(0) accepted")
	}
	win, _ := window.New(16)
	if _, err := match.New(win, 0, 16); err == nil {
		t.Fatal("match.New maxChain=0 accepted")
	}
	if _, err := enc.NewWriter(&buf, &enc.Config{Window: 0, MaxChain: 1}); err == nil {
		t.Fatal("enc: window=0 accepted")
	}
	if _, err := enc.NewWriter(&buf, &enc.Config{Window: 16, MaxChain: 0}); err == nil {
		t.Fatal("enc: maxChain=0 accepted")
	}
}
