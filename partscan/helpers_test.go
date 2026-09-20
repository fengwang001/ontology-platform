package partscan

import (
	"bytes"
	"fmt"
	"testing"
)

// buildStream assembles a valid delimited stream from raw part contents.
func buildStream(boundary string, parts [][]byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("--")
	buf.WriteString(boundary)
	buf.WriteString("\r\n")
	sep := []byte("\r\n--" + boundary + "\r\n")
	closer := []byte("\r\n--" + boundary + "--")
	for i, p := range parts {
		buf.Write(p)
		if i == len(parts)-1 {
			buf.Write(closer)
		} else {
			buf.Write(sep)
		}
	}
	return buf.Bytes()
}

// feedBySizes feeds data split into the given successive chunk sizes
// (last chunk absorbs the remainder) and collects emitted parts.
func feedBySizes(s *Scanner, data []byte, sizes ...int) ([][]byte, error) {
	var got [][]byte
	off := 0
	for _, n := range sizes {
		end := off + n
		if end > len(data) {
			end = len(data)
		}
		ps, err := s.Feed(data[off:end])
		got = append(got, ps...)
		if err != nil {
			return got, err
		}
		off = end
	}
	if off < len(data) {
		ps, err := s.Feed(data[off:])
		got = append(got, ps...)
		if err != nil {
			return got, err
		}
	}
	return got, nil
}

func assertParts(t *testing.T, got, want [][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("part count: got %d (%s), want %d (%s)",
			len(got), summarize(got), len(want), summarize(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("part %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func summarize(ps [][]byte) string {
	return fmt.Sprintf("%q", ps)
}
