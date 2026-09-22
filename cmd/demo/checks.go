package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"ontology/coalesce"
	"ontology/rangespec"
	"ontology/serve"
	"ontology/source"
	"reflect"
)

var demoData = source.Bytes([]byte("0123456789"))

func bodyOf(a *serve.Assembler) ([]byte, error) {
	var buf bytes.Buffer
	_, err := a.WriteTo(&buf)
	return buf.Bytes(), err
}

func mustAssemble(cfg serve.Config, src source.Source, header string) ([]byte, *serve.Assembler, error) {
	a := serve.NewAssembler(cfg, src)
	if err := a.Assemble(header); err != nil {
		return nil, nil, err
	}
	b, err := bodyOf(a)
	return b, a, err
}

func checkForms() (string, error) {
	cases := []struct{ header, want string }{
		{"bytes=2-5", "2345"},
		{"bytes=2-999", "23456789"},
		{"bytes=7-", "789"},
		{"bytes=-3", "789"},
		{"bytes=-999", "0123456789"},
	}
	for _, c := range cases {
		got, _, err := mustAssemble(serve.Config{}, demoData, c.header)
		if err != nil {
			return "", err
		}
		if string(got) != c.want {
			return "", fmt.Errorf("%s = %q, want %q", c.header, got, c.want)
		}
	}
	return "", nil
}

func checkMinusZero() (string, error) {
	a := serve.NewAssembler(serve.Config{}, demoData)
	err := a.Assemble("bytes=-0")
	var ue *coalesce.UnsatisfiableError
	if !errors.As(err, &ue) || ue.Size != 10 {
		return "", fmt.Errorf("got %v, want UnsatisfiableError{Size:10}", err)
	}
	return "", nil
}

func checkErrorKinds() (string, error) {
	a := serve.NewAssembler(serve.Config{}, demoData)
	synErr := a.Assemble("bytes=2x-5")
	var se *rangespec.SyntaxError
	if !errors.As(synErr, &se) || se.Offset != 7 {
		return "", fmt.Errorf("syntax: %v", synErr)
	}
	var ue *coalesce.UnsatisfiableError
	if errors.As(synErr, &ue) {
		return "", fmt.Errorf("syntax error matches unsatisfiable")
	}
	unsatErr := a.Assemble("bytes=100-200")
	if !errors.As(unsatErr, &ue) || errors.As(unsatErr, &se) {
		return "", fmt.Errorf("unsatisfiable: %v", unsatErr)
	}
	return fmt.Sprintf("syntax offset=%d, unsatisfiable size=%d", se.Offset, ue.Size), nil
}

// clip mirrors the coalesce clipping rules to build the pre-merge byte set.
func clipSet(sp rangespec.Spec, size int64, set map[int64]bool) {
	last := size - 1
	var from, to int64
	switch sp.Kind {
	case rangespec.Span:
		from, to = sp.From, sp.To
		if to > last {
			to = last
		}
	case rangespec.Open:
		from, to = sp.From, last
	case rangespec.Suffix:
		from, to = 0, last
		if sp.N < size {
			from = size - sp.N
		}
		if sp.N == 0 {
			return
		}
	}
	for i := from; i <= to && i <= last; i++ {
		if i >= 0 {
			set[i] = true
		}
	}
}

func checkByteSet() (string, error) {
	rng := rand.New(rand.NewSource(7))
	const size = 32
	for iter := 0; iter < 2000; iter++ {
		n := 1 + rng.Intn(8)
		specs := make([]rangespec.Spec, n)
		before := map[int64]bool{}
		for i := range specs {
			switch rng.Intn(3) {
			case 0:
				a := rng.Int63n(size + 2)
				specs[i] = rangespec.Spec{Kind: rangespec.Span, From: a, To: a + rng.Int63n(20)}
			case 1:
				specs[i] = rangespec.Spec{Kind: rangespec.Open, From: rng.Int63n(size + 2)}
			default:
				specs[i] = rangespec.Spec{Kind: rangespec.Suffix, N: 1 + rng.Int63n(size+2)}
			}
			clipSet(specs[i], size, before)
		}
		rs, err := coalesce.Normalize(specs, size)
		if err != nil {
			if len(before) != 0 {
				return "", fmt.Errorf("iter %d: %v but bytes covered", iter, err)
			}
			continue
		}
		after := map[int64]bool{}
		for _, r := range rs {
			for i := r.From; i <= r.To; i++ {
				after[i] = true
			}
		}
		if !reflect.DeepEqual(before, after) {
			return "", fmt.Errorf("iter %d: byte set changed by merge", iter)
		}
	}
	return "2000 random spec sets", nil
}
