package main

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"ontology/coalesce"
	"ontology/rangespec"
	"ontology/serve"
	"ontology/source"
)

func checkComplexity() (string, error) {
	counts := map[int]int64{}
	for _, n := range []int{100, 10000} {
		rng := rand.New(rand.NewSource(int64(n)))
		specs := make([]rangespec.Spec, n)
		for i := range specs {
			a := rng.Int63n(1 << 30)
			specs[i] = rangespec.Spec{Kind: rangespec.Span, From: a, To: a + rng.Int63n(1<<28)}
		}
		coalesce.ResetCompareCount()
		if _, err := coalesce.Normalize(specs, 1<<30); err != nil {
			return "", err
		}
		counts[n] = coalesce.CompareCount()
	}
	ratio := float64(counts[10000]) / float64(counts[100])
	bound := 2 * (10000 * math.Log2(10000)) / (100 * math.Log2(100))
	if ratio > bound {
		return "", fmt.Errorf("ratio %.1f exceeds n log n bound %.1f", ratio, bound)
	}
	return fmt.Sprintf("c(100)=%d c(10000)=%d ratio=%.1f bound=%.1f",
		counts[100], counts[10000], ratio, bound), nil
}

func checkShortRead() (string, error) {
	data := []byte("0123456789abcdef")
	flaky := &source.Flaky{Src: source.Bytes(data), MaxChunk: 1}
	got, _, err := mustAssemble(serve.Config{}, flaky, "bytes=0-15")
	if err != nil {
		return "", err
	}
	if !bytes.Equal(got, data) {
		return "", fmt.Errorf("got %q", got)
	}
	// EOF before the range is filled must be a distinguishable error.
	shrunk := &source.Flaky{
		Src: source.Bytes(make([]byte, 100)), HasFakeSize: true, FakeSize: 100,
		HasTrunc: true, TruncAt: 40,
	}
	err = serve.NewAssembler(serve.Config{}, shrunk).Assemble("bytes=0-99")
	if !errors.Is(err, serve.ErrShortData) {
		return "", fmt.Errorf("short data: %v", err)
	}
	return "", nil
}

type chunkWriter struct {
	max int
	buf bytes.Buffer
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	if len(p) > w.max {
		p = p[:w.max]
	}
	return w.buf.Write(p)
}

func fixedRand() *bytes.Reader { return bytes.NewReader([]byte("0123456789abcdef")) }

func checkSplitPoints() (string, error) {
	data := source.Bytes([]byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"))
	header := "bytes=0-5, 10-19, 30-35"
	ref, _, err := mustAssemble(serve.Config{Rand: fixedRand()}, data, header)
	if err != nil {
		return "", err
	}
	for chunk := 1; chunk <= len(ref); chunk++ {
		a := serve.NewAssembler(serve.Config{Rand: fixedRand()}, data)
		if err := a.Assemble(header); err != nil {
			return "", err
		}
		w := &chunkWriter{max: chunk}
		for a.Written() < a.Total() {
			if _, err := a.WriteTo(w); err != nil {
				return "", err
			}
		}
		if !bytes.Equal(w.buf.Bytes(), ref) {
			return "", fmt.Errorf("chunk=%d differs", chunk)
		}
	}
	return fmt.Sprintf("%d split points", len(ref)), nil
}

func checkBareBytes() (string, error) {
	got, a, err := mustAssemble(serve.Config{}, demoData, "bytes=2-4, 5-7")
	if err != nil {
		return "", err
	}
	if a.IsMultipart() || string(got) != "234567" {
		return "", fmt.Errorf("merged-to-one must be bare bytes, got %q multi=%v", got, a.IsMultipart())
	}
	return "", nil
}
