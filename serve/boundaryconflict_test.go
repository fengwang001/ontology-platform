package serve_test

import (
	"bytes"
	"strings"
	"testing"

	"ontology/multipart"
	"ontology/source"
)

// 内容里刻意放入大量带固定前缀、带前导 "--" 的伪边界字节。
// 最终选中的边界串绝不能与内容冲突。
func TestBoundaryChosenDespiteLookalikeContent(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("--ontologyBR")
		b.WriteString(strings.Repeat("X", 32))
		b.WriteString("\r\n")
	}
	data := []byte(b.String())

	a := build(t, "bytes=0-"+itoa(len(data)-1), source.NewMemory(data))
	// 单区间是裸字节；这里强制两区间以走封装路径。
	// 取两段不相邻的区间，确保合并后仍是两个区间，从而强制封装。
	third := len(data) / 3
	a = build(t, "bytes=0-"+itoa(third-1)+","+itoa(2*third)+"-"+itoa(len(data)-1),
		source.NewMemory(data))
	if !a.IsMultipart() {
		t.Fatal("expected multipart")
	}
	if multipart.ContainsBoundary(data, a.Boundary()) {
		t.Fatalf("chosen boundary %q appears in resource content", a.Boundary())
	}
	body := drainAll(t, a)
	if multipart.ContainsBoundary(data, a.Boundary()) {
		t.Fatal("boundary collides with payload")
	}
	if !bytes.HasSuffix(body, multipart.EndBoundary(a.Boundary())) {
		t.Fatal("missing end boundary")
	}
}
