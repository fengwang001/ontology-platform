// Command demo self-checks each documented partscan semantic and prints one
// OK/FAIL line per semantic. It exits 0 only when every check passes.
package main

import (
	"bytes"
	"fmt"
	"os"

	"ontology/partscan"
)

func main() {
	failed := false
	checks := []struct {
		name string
		fn   func() error
	}{
		{"1 split invariance", checkSplitInvariance},
		{"2 delimiter prefixes preserved", checkPrefixes},
		{"3 empty parts", checkEmpty},
		{"4 preamble validation", checkPreamble},
		{"5 closer and after-close", checkCloser},
		{"6 incomplete close", checkIncomplete},
		{"7 copy isolation", checkIsolation},
		{"8 bounded pending", checkBounded},
	}
	for _, c := range checks {
		if err := c.fn(); err != nil {
			fmt.Printf("FAIL %s: %v\n", c.name, err)
			failed = true
		} else {
			fmt.Printf("OK   %s\n", c.name)
		}
	}
	if failed {
		os.Exit(1)
	}
}

func buildStream(boundary string, parts [][]byte) []byte {
	var b bytes.Buffer
	b.WriteString("--" + boundary + "\r\n")
	sep := "\r\n--" + boundary + "\r\n"
	closeSeq := "\r\n--" + boundary + "--"
	for i, p := range parts {
		b.Write(p)
		if i == len(parts)-1 {
			b.WriteString(closeSeq)
		} else {
			b.WriteString(sep)
		}
	}
	return b.Bytes()
}

func feedSplit(data []byte, sizes []int) ([][]byte, *partscan.Scanner, error) {
	s := partscan.New("B")
	var out [][]byte
	off := 0
	for _, n := range sizes {
		end := off + n
		if end > len(data) {
			end = len(data)
		}
		ps, err := s.Feed(data[off:end])
		out = append(out, ps...)
		if err != nil {
			return out, s, err
		}
		off = end
	}
	if off < len(data) {
		ps, err := s.Feed(data[off:])
		out = append(out, ps...)
		if err != nil {
			return out, s, err
		}
	}
	return out, s, nil
}

func equalParts(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}
