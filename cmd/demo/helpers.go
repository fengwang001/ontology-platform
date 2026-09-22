package main

import (
	"errors"
	"math/rand/v2"

	"ontology/coalesce"
	"ontology/rangespec"
	"ontology/serve"
	"ontology/source"
)

func toSpecs(ivs []coalesce.Interval) []rangespec.Spec {
	out := make([]rangespec.Spec, len(ivs))
	for i, iv := range ivs {
		out[i] = rangespec.Spec{Kind: rangespec.FromTo, Start: iv.Start, End: iv.End}
	}
	return out
}

// setEqualDemo 做 2000 组随机集合，位图对照合并前后覆盖字节。
func setEqualDemo() bool {
	const U = 64
	rng := rand.New(rand.NewPCG(7, 9))
	for it := 0; it < 2000; it++ {
		ivs := make([]coalesce.Interval, 0, 8)
		before := make([]bool, U)
		for k := 0; k < 8; k++ {
			a, b := rng.IntN(U), rng.IntN(U)
			if a > b {
				a, b = b, a
			}
			ivs = append(ivs, coalesce.Interval{Start: int64(a), End: int64(b)})
			for p := a; p <= b; p++ {
				before[p] = true
			}
		}
		out, err := coalesce.Normalize(toSpecs(ivs), U)
		if err != nil {
			return false
		}
		after := make([]bool, U)
		for _, iv := range out {
			for p := iv.Start; p <= iv.End; p++ {
				after[p] = true
			}
		}
		for p := 0; p < U; p++ {
			if before[p] != after[p] {
				return false
			}
		}
	}
	return true
}

func cmpDemo(n int) int64 {
	rng := rand.New(rand.NewPCG(uint64(n), 3))
	ivs := make([]coalesce.Interval, 0, n)
	for i := 0; i < n; i++ {
		a, b := rng.IntN(n*10), rng.IntN(n*10)
		if a > b {
			a, b = b, a
		}
		ivs = append(ivs, coalesce.Interval{Start: int64(a), End: int64(b)})
	}
	nr := coalesce.NewNormalizer()
	if _, err := nr.Normalize(toSpecs(ivs), int64(n*10)); err != nil {
		return -1
	}
	return nr.CompareCount()
}

type memWriter struct{ b []byte }

func (w *memWriter) Write(p []byte) (int, error) {
	n := 1
	w.b = append(w.b, p[:n]...)
	return n, nil
}

func drain(a *serve.Assembler) []byte {
	w := &memWriter{}
	for !a.Done() {
		if _, err := a.WriteTo(w); err != nil {
			return nil
		}
	}
	return w.b
}

func buildOne(data []byte) *serve.Assembler {
	a := serve.New(serve.Config{})
	_ = a.Build("bytes=0-0", source.NewMemory(data))
	return a
}

// splitDemo 用参考体对照每个固定切分长度拼回的字节。
func splitDemo(data []byte) bool {
	hdr := "bytes=0-9,20-29,45-59"
	ref := serve.New(serve.Config{})
	if err := ref.Build(hdr, source.NewMemory(data)); err != nil {
		return false
	}
	want := drainAllBytes(ref)
	for step := 1; step <= len(want); step++ {
		a := serve.New(serve.Config{})
		if err := a.Build(hdr, source.NewMemory(data)); err != nil {
			return false
		}
		w := &stepWriter{step: step}
		for !a.Done() {
			if _, err := a.WriteTo(w); err != nil {
				return false
			}
		}
		if len(w.b) != len(want) {
			return false
		}
		for i := range want {
			if w.b[i] != want[i] {
				return false
			}
		}
	}
	return true
}

type stepWriter struct {
	step int
	b    []byte
}

func (w *stepWriter) Write(p []byte) (int, error) {
	n := w.step
	if n > len(p) {
		n = len(p)
	}
	w.b = append(w.b, p[:n]...)
	return n, nil
}

func drainAllBytes(a *serve.Assembler) []byte {
	w := &stepWriter{step: 1 << 20}
	for !a.Done() {
		a.WriteTo(w)
	}
	return w.b
}

func limitsDemo(data []byte) bool {
	a := serve.New(serve.Config{MaxRanges: 2, MaxResponseBytes: 50})
	if err := a.Build("bytes=0-9", source.NewMemory(data)); err != nil {
		return false
	}
	before := a.TotalSize()
	e1 := a.Build("bytes=0-1,2-3,4-5", source.NewMemory(data))
	e2 := a.Build("bytes=0-199", source.NewMemory(data))
	distinct := !errors.Is(serve.ErrTooManyRanges, serve.ErrResponseTooLarge) &&
		!errors.Is(serve.ErrTooManyRanges, serve.ErrBoundaryAttempts) &&
		!errors.Is(serve.ErrResponseTooLarge, serve.ErrBoundaryAttempts)
	return errors.Is(e1, serve.ErrTooManyRanges) &&
		errors.Is(e2, serve.ErrResponseTooLarge) &&
		a.TotalSize() == before && distinct
}
