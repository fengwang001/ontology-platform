package partscan

import (
	"bytes"
	"math/rand"
	"testing"
)

func encode(boundary string, parts [][]byte) []byte {
	var b bytes.Buffer
	b.WriteString("--" + boundary + "\r\n")
	for i, p := range parts {
		b.Write(p)
		if i < len(parts)-1 {
			b.WriteString("\r\n--" + boundary + "\r\n")
		}
	}
	b.WriteString("\r\n--" + boundary + "--")
	return b.Bytes()
}

func runChunks(t *testing.T, boundary string, data []byte, sizes []int) [][]byte {
	t.Helper()
	s := New(boundary)
	var got [][]byte
	pos := 0
	for k := 0; pos < len(data); k++ {
		n := sizes[k%len(sizes)]
		if n > len(data)-pos {
			n = len(data) - pos
		}
		ps, err := s.Feed(append([]byte(nil), data[pos:pos+n]...))
		if err != nil {
			t.Fatalf("Feed error: %v", err)
		}
		got = append(got, ps...)
		pos += n
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	return got
}

func expectParts(t *testing.T, got, want [][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("part count = %d, want %d; got: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] == nil || !bytes.Equal(got[i], want[i]) {
			t.Fatalf("part %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func sampleParts() [][]byte {
	return [][]byte{
		[]byte("hello"),
		{},
		[]byte("\r\n--bou"),
		[]byte("x\r\n-b\r\n--bZ"),
		[]byte("tail"),
	}
}

// 语义 1：切法不变性。
func TestSplitInvariance(t *testing.T) {
	boundary := "b"
	parts := sampleParts()
	stream := encode(boundary, parts)

	want := make([][]byte, len(parts))
	for i, p := range parts {
		want[i] = append([]byte(nil), p...)
	}

	sizes := [][]int{
		{len(stream)},
		{1},
		{2},
		{3},
		{7},
		{1000},
		// 把 "\r\n--b" 恰好切成 "\r\n-" / "-b"
		{len("--b\r\n") + len("hello") + 3, 2, 1, 50},
	}
	for _, sz := range sizes {
		got := runChunks(t, boundary, stream, sz)
		expectParts(t, got, want)
	}
}

func TestRandomChunks(t *testing.T) {
	boundary := "xyz"
	parts := sampleParts()
	stream := encode(boundary, parts)
	want := flattenParts(parts)

	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 300; iter++ {
		var sz []int
		for range 12 {
			sz = append(sz, 1+r.Intn(9))
		}
		got := runChunks(t, boundary, stream, sz)
		expectParts(t, got, want)
	}
}

func flattenParts(parts [][]byte) [][]byte {
	out := make([][]byte, len(parts))
	for i, p := range parts {
		out[i] = append([]byte(nil), p...)
	}
	return out
}

// 语义 2：段内部分匹配前缀原样保留。
func TestPartialMatchKept(t *testing.T) {
	parts := [][]byte{
		[]byte("a\r\n-b"),
		[]byte("\r\n--bou"),
		[]byte("\r\n--bX"),
	}
	stream := encode("b", parts)
	for _, sz := range [][]int{{1}, {len(stream)}, {4}} {
		got := runChunks(t, "b", stream, sz)
		expectParts(t, got, flattenParts(parts))
	}
}

// 语义 3：空段。
func TestEmptyParts(t *testing.T) {
	parts := [][]byte{{}, {}, []byte("x"), {}}
	stream := encode("b", parts)
	for _, sz := range [][]int{{1}, {len(stream)}, {5}} {
		got := runChunks(t, "b", stream, sz)
		expectParts(t, got, flattenParts(parts))
	}
}
