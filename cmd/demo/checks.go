package main

import (
	"bytes"
	"errors"
	"math/rand"
	"strings"

	"ontology/coalesce"
	"ontology/multipart"
	"ontology/rangespec"
	"ontology/serve"
	"ontology/source"
)

// byteSetCheck expands random spec sets to byte sets before and after
// normalization and requires them to be identical.
func byteSetCheck() bool {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 200; trial++ {
		total := int64(1 + rng.Intn(100))
		var specs []rangespec.Spec
		for i := 0; i < 1+rng.Intn(15); i++ {
			first := int64(rng.Intn(int(total)))
			specs = append(specs, rangespec.Spec{
				First:  first,
				Last:   first + int64(rng.Intn(int(total))),
				Suffix: -1,
			})
		}
		before := map[int64]bool{}
		for _, s := range specs {
			rs, err := coalesce.Normalize([]rangespec.Spec{s}, total)
			if err != nil {
				continue
			}
			for b := rs[0].First; b <= rs[0].Last; b++ {
				before[b] = true
			}
		}
		merged, err := coalesce.Normalize(specs, total)
		if err != nil {
			return len(before) == 0
		}
		after := map[int64]bool{}
		for _, r := range merged {
			for b := r.First; b <= r.Last; b++ {
				after[b] = true
			}
		}
		if len(before) != len(after) {
			return false
		}
		for b := range before {
			if !after[b] {
				return false
			}
		}
	}
	return true
}

// countComparisons returns the comparison count for normalizing n ranges.
func countComparisons(n int) int64 {
	rng := rand.New(rand.NewSource(int64(n)))
	specs := make([]rangespec.Spec, n)
	for i := range specs {
		first := int64(rng.Intn(n))
		specs[i] = rangespec.Spec{First: first, Last: first + int64(rng.Intn(n)), Suffix: -1}
	}
	coalesce.ResetCompareCount()
	if _, err := coalesce.Normalize(specs, int64(2*n)); err != nil {
		return -1
	}
	return coalesce.CompareCount()
}

// splitPointCheck writes one response at every chunk size from 1 to the
// total length and requires byte-identical output.
func splitPointCheck() bool {
	data := source.Bytes([]byte("the quick brown fox jumps over the lazy dog"))
	a := &serve.Assembler{Src: data, Rand: zeroRand{}}
	const header = "bytes=0-9, 16-24, 30-42"
	r, err := a.Build(header)
	if err != nil {
		return false
	}
	var want bytes.Buffer
	drain(r, &want)
	total := int(r.TotalBytes())
	for chunk := 1; chunk <= total; chunk++ {
		r2, err := a.Build(header)
		if err != nil {
			return false
		}
		w := &chunkWriter{max: chunk}
		drain(r2, w)
		if !bytes.Equal(w.buf.Bytes(), want.Bytes()) {
			return false
		}
	}
	return true
}

// boundaryCheck builds a multipart response over content that contains a
// boundary-looking decoy and requires no collision.
func boundaryCheck() bool {
	decoy := multipart.BoundaryPrefix + strings.Repeat("ab", 20)
	data := source.Bytes([]byte("AA--" + decoy + "--BB"))
	a := &serve.Assembler{Src: data}
	r, err := a.Build("bytes=0-3, 8-11")
	if err != nil || !r.Multipart() {
		return false
	}
	var buf bytes.Buffer
	drain(r, &buf)
	body := buf.Bytes()
	line := string(body[:bytes.IndexByte(body, '\r')])
	boundary := strings.TrimPrefix(line, "--")
	return boundary != "" && !strings.Contains(string(data), boundary)
}

// limitsCheck triggers all three limits and requires distinguishable errors.
func limitsCheck() bool {
	// Content is saturated with the exact candidate zeroRand produces, so
	// boundary generation always collides and exhausts its retries.
	candidate := multipart.BoundaryPrefix + strings.Repeat("0", 32)
	data := source.Bytes([]byte(strings.Repeat(candidate, 30)))
	a := &serve.Assembler{
		Src:    data,
		Limits: serve.Limits{MaxRanges: 2, MaxBytes: 500, MaxBoundaryTry: 1},
		Rand:   zeroRand{}, // candidate "ontology-000...0" occurs in content
	}
	_, err1 := a.Build("bytes=0-1, 2-3, 6-7")
	_, err2 := a.Build("bytes=0-500")
	// The requested bytes span one full candidate occurrence, so every
	// boundary attempt collides with the content.
	_, err3 := a.Build("bytes=0-9, 41-81")
	return errors.Is(err1, serve.ErrTooManyRanges) &&
		errors.Is(err2, serve.ErrTooLarge) &&
		errors.Is(err3, multipart.ErrBoundaryExhausted) &&
		!errors.Is(err1, serve.ErrTooLarge) &&
		!errors.Is(err2, serve.ErrTooManyRanges) &&
		!errors.Is(err3, serve.ErrTooManyRanges)
}
