package serve_test

import (
	"bytes"
	"strings"
	"testing"

	"ontology/multipart"
	"ontology/source"
)

func TestSingleRangeIsRawBytes(t *testing.T) {
	data := makeData(50)
	a := build(t, "bytes=10-19", source.NewMemory(data))
	if a.IsMultipart() {
		t.Fatal("single range must not be multipart")
	}
	got := drainAll(t, a)
	if !bytes.Equal(got, data[10:20]) {
		t.Fatal("single range body must be raw bytes")
	}
	if a.ContentType() != "application/octet-stream" {
		t.Fatalf("content type=%q", a.ContentType())
	}
}

func TestTwoMergedIntoOneDegeneratesToRaw(t *testing.T) {
	data := makeData(50)
	// 两个相邻区间 [0,9] 与 [10,19] 合并为一个，封装必须退化为裸字节。
	a := build(t, "bytes=0-9,10-19", source.NewMemory(data))
	if len(a.Ranges()) != 1 {
		t.Fatalf("ranges=%v want single merged", a.Ranges())
	}
	if a.IsMultipart() {
		t.Fatal("merged-into-one must be raw")
	}
	got := drainAll(t, a)
	if !bytes.Equal(got, data[0:20]) {
		t.Fatalf("merged raw body mismatch: %v", got)
	}
}

func TestTwoRangesAreMultipart(t *testing.T) {
	data := makeData(50)
	a := build(t, "bytes=0-9,20-29", source.NewMemory(data))
	if !a.IsMultipart() {
		t.Fatal("two disjoint ranges must be multipart")
	}
	body := drainAll(t, a)
	if !strings.Contains(string(body), "Content-Range: bytes 0-9/50") {
		t.Fatal("missing first part header")
	}
	if !strings.Contains(string(body), "Content-Range: bytes 20-29/50") {
		t.Fatal("missing second part header")
	}
	if !bytes.HasSuffix(body, multipart.EndBoundary(a.Boundary())) {
		t.Fatal("missing end boundary")
	}
	// 定界行 "--boundary" 应恰好出现 3 次：两个段头 + 一个结束边界；
	// 若内容字节里也含该串，计数会大于 3。
	delim := []byte("--" + a.Boundary())
	count := 0
	for rest := body; ; {
		i := bytes.Index(rest, delim)
		if i < 0 {
			break
		}
		count++
		rest = rest[i+len(delim):]
	}
	if count != 3 {
		t.Fatalf("delimiter occurs %d times, want exactly 3 (leaks into content?)", count)
	}
}
