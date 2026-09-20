// Command demo self-checks each documented partscan semantic and prints one
// OK/FAIL line per semantic. It exits 0 only when every check passes.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"

	"ontology/partscan"
)

func checkSplitInvariance() error {
	parts := [][]byte{
		[]byte("hello"),
		[]byte("\r\n-\r\n--Bxx"),
		bytes.Repeat([]byte("q"), 300),
		[]byte(""),
		[]byte("end"),
	}
	data := buildStream("B", parts)

	ref, _, err := feedSplit(data, []int{len(data)})
	if err != nil {
		return err
	}
	splits := [][]int{{1}, {2}, {3}, {5}, {len(data)}}
	for cut := 1; cut < len(data); cut++ {
		splits = append(splits, []int{cut, len(data) - cut})
	}
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 100; iter++ {
		sizes := []int{}
		n := 1 + rng.Intn(12)
		sizes = append(sizes, n)
		splits = append(splits, sizes)
	}
	for i, sz := range splits {
		got, s, err := feedSplit(data, sz)
		if err != nil {
			return fmt.Errorf("split %d: %w", i, err)
		}
		if !s.Done() || !equalParts(got, ref) {
			return fmt.Errorf("split %d mismatch", i)
		}
	}
	return nil
}

func checkPrefixes() error {
	inner := []byte("a\r\n--BX\r\n--B-\r\n--B\rX")
	data := buildStream("B", [][]byte{inner, []byte("z")})
	got, s, err := feedSplit(data, []int{1})
	if err != nil || !s.Done() {
		return fmt.Errorf("err=%v done=%v", err, s.Done())
	}
	if !equalParts(got, [][]byte{inner, []byte("z")}) {
		return errors.New("prefix bytes altered")
	}
	return nil
}

func checkEmpty() error {
	data := []byte("--B\r\n\r\n--B\r\n\r\n--B--")
	got, s, err := feedSplit(data, []int{1})
	if err != nil || !s.Done() {
		return fmt.Errorf("err=%v done=%v", err, s.Done())
	}
	if len(got) != 2 {
		return fmt.Errorf("got %d parts", len(got))
	}
	for i, p := range got {
		if p == nil || len(p) != 0 {
			return fmt.Errorf("part %d not non-nil empty", i)
		}
	}
	return nil
}

func checkPreamble() error {
	s := partscan.New("B")
	if _, err := s.Feed([]byte("XX")); !errors.Is(err, partscan.ErrNoPreamble) {
		return fmt.Errorf("got %v", err)
	}
	if _, err := s.Feed([]byte("--B\r\n")); !errors.Is(err, partscan.ErrNoPreamble) {
		return fmt.Errorf("sticky error got %v", err)
	}
	return nil
}

func checkCloser() error {
	data := []byte("--B\r\np\r\n--B--")
	s := partscan.New("B")
	got, err := s.Feed(append(data, []byte("tail")...))
	if err != nil || !s.Done() || !equalParts(got, [][]byte{[]byte("p")}) {
		return fmt.Errorf("err=%v done=%v parts=%q", err, s.Done(), got)
	}
	if err := s.Close(); err != nil {
		return err
	}
	if _, err := s.Feed([]byte("x")); !errors.Is(err, partscan.ErrAfterClose) {
		return fmt.Errorf("late feed got %v", err)
	}
	return nil
}

func checkIncomplete() error {
	s := partscan.New("B")
	if _, err := s.Feed([]byte("--B\r\nabc")); err != nil {
		return err
	}
	if err := s.Close(); !errors.Is(err, partscan.ErrIncomplete) {
		return fmt.Errorf("got %v", err)
	}
	if err := s.Close(); !errors.Is(err, partscan.ErrIncomplete) {
		return fmt.Errorf("repeat got %v", err)
	}
	return nil
}

func checkIsolation() error {
	data := []byte("--B\r\nsec\r\n--B--")
	s := partscan.New("B")
	buf := make([]byte, 1)
	var got [][]byte
	for _, c := range data {
		buf[0] = c
		ps, err := s.Feed(buf)
		if err != nil {
			return err
		}
		got = append(got, ps...)
		buf[0] = 0xFF
	}
	if !equalParts(got, [][]byte{[]byte("sec")}) {
		return errors.New("caller mutation leaked in")
	}
	for i := range got[0] {
		got[0][i] = 0
	}
	if err := s.Close(); err != nil {
		return err
	}
	return nil
}

func checkBounded() error {
	s := partscan.New("B")
	if _, err := s.Feed([]byte("--B\r\n")); err != nil {
		return err
	}
	for i := 0; i < 10000; i++ {
		var chunk []byte
		if i == 9999 {
			chunk = []byte("p\r\n--B--")
		} else {
			chunk = []byte("p\r\n--B\r\n")
		}
		ps, err := s.Feed(chunk)
		if err != nil || len(ps) != 1 || string(ps[0]) != "p" {
			return fmt.Errorf("iter %d err=%v ps=%q", i, err, ps)
		}
		if i != 9999 && s.Pending() != 0 {
			return fmt.Errorf("iter %d pending %d", i, s.Pending())
		}
	}
	if !s.Done() || s.Pending() != 0 || s.Close() != nil {
		return errors.New("final state wrong")
	}
	return nil
}
