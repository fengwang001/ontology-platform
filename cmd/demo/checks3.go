package main

import (
	"bytes"
	"errors"
	"fmt"
	"ontology/serve"
	"ontology/source"
	"reflect"
)

// scriptRand replays 16-byte chunks in order, then repeats the last one.
type scriptRand struct {
	chunks [][]byte
	pos    int
}

func (s *scriptRand) Read(p []byte) (int, error) {
	idx := s.pos / 16
	if idx >= len(s.chunks) {
		idx = len(s.chunks) - 1
	}
	n := copy(p, s.chunks[idx][s.pos%16:])
	s.pos += n
	return n, nil
}

func checkBoundary() (string, error) {
	evil := []byte("EVILBOUNDARY0001")
	good := []byte("GOODBOUNDARY0002")
	evilToken := "ontology-" + hexOf(evil)
	data := source.Bytes([]byte("<<" + evilToken + ">>" + evilToken + "<<"))
	rand := &scriptRand{chunks: [][]byte{evil, good}}
	a := serve.NewAssembler(serve.Config{Rand: rand, MaxBoundaryTries: 4}, data)
	if err := a.Assemble("bytes=0-42, 45-87"); err != nil {
		return "", err
	}
	if a.Boundary() == evilToken {
		return "", fmt.Errorf("boundary %q collides with payload", evilToken)
	}
	body, err := bodyOf(a)
	if err != nil {
		return "", err
	}
	if !bytes.Contains(body, []byte(evilToken)) {
		return "", fmt.Errorf("payload content not preserved")
	}
	return fmt.Sprintf("boundary=%s...", a.Boundary()[:17]), nil
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = digits[c>>4]
		out[i*2+1] = digits[c&0xf]
	}
	return string(out)
}

func checkLimits() (string, error) {
	a := serve.NewAssembler(serve.Config{MaxRanges: 1}, demoData)
	if err := a.Assemble("bytes=0-1, 2-3"); !errors.Is(err, serve.ErrTooManyRanges) {
		return "", fmt.Errorf("range limit: %v", err)
	}
	if a.Total() != 0 || a.Ranges() != nil {
		return "", fmt.Errorf("range-limit rejection mutated state")
	}
	b := serve.NewAssembler(serve.Config{MaxBytes: 5}, demoData)
	if err := b.Assemble("bytes=0-9"); !errors.Is(err, serve.ErrResponseTooLarge) {
		return "", fmt.Errorf("byte limit: %v", err)
	}
	if b.Total() != 0 {
		return "", fmt.Errorf("byte-limit rejection mutated state")
	}
	token := "ontology-30313233343536373839616263646566"
	src := source.Bytes([]byte(token + "PAD" + token))
	rand := bytes.NewReader(bytes.Repeat([]byte("0123456789abcdef"), 8))
	c := serve.NewAssembler(serve.Config{MaxBoundaryTries: 2, Rand: rand}, src)
	if err := c.Assemble("bytes=0-40, 44-84"); !errors.Is(err, serve.ErrBoundaryExhausted) {
		return "", fmt.Errorf("boundary limit: %v", err)
	}
	if c.Total() != 0 || c.Boundary() != "" {
		return "", fmt.Errorf("boundary-limit rejection mutated state")
	}
	return "", nil
}

func checkQueries() (string, error) {
	a := serve.NewAssembler(serve.Config{Rand: fixedRand()}, demoData)
	if a.Ranges() != nil || a.Total() != 0 || a.Written() != 0 || a.IsMultipart() {
		return "", fmt.Errorf("pre-assembly queries not zero")
	}
	if err := a.Assemble("bytes=0-3, 6-9"); err != nil {
		return "", err
	}
	type snap struct {
		r     interface{}
		total int64
		w     int64
		multi bool
	}
	s1 := snap{a.Ranges(), a.Total(), a.Written(), a.IsMultipart()}
	s2 := snap{a.Ranges(), a.Total(), a.Written(), a.IsMultipart()}
	if !reflect.DeepEqual(s1, s2) {
		return "", fmt.Errorf("consecutive queries differ")
	}
	return "", nil
}
