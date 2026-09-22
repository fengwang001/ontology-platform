package multipart_test

import (
	"bytes"
	"strings"
	"testing"

	"ontology/multipart"
)

func TestChooseBoundaryAbsentFromContent(t *testing.T) {
	// 内容里塞了大量"看起来像边界串"的字节：既有固定前缀，也有
	// "--ontologyBR..." 形式的定界行，最终边界必须与它们都不冲突。
	var b strings.Builder
	b.WriteString("prefix --ontologyBRAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\r\n")
	b.WriteString("--ontologyRBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB junk\r\n")
	for i := 0; i < 100; i++ {
		b.WriteString("--ontologyPR fake delimiter noise\n")
	}
	content := []byte(b.String())

	boundary, err := multipart.ChooseBoundary(content, 16)
	if err != nil {
		t.Fatalf("choose boundary: %v", err)
	}
	if multipart.ContainsBoundary(content, boundary) {
		t.Fatalf("chosen boundary %q collides with content", boundary)
	}
}

func TestFramingLayout(t *testing.T) {
	// 手工校验多段封装的字节布局与结束边界。
	b := "BND"
	hdr := multipart.PartHeader(b, iv(0, 2), 10)
	if !bytes.HasPrefix(hdr, []byte("--BND\r\n")) {
		t.Fatalf("bad header: %q", hdr)
	}
	if !bytes.Contains(hdr, []byte("Content-Range: bytes 0-2/10\r\n\r\n")) {
		t.Fatalf("bad content-range: %q", hdr)
	}
	end := multipart.EndBoundary(b)
	if string(end) != "--BND--\r\n" {
		t.Fatalf("bad end boundary: %q", end)
	}
	if multipart.ContentType(b) != "multipart/byteranges; boundary=BND" {
		t.Fatal("bad content type")
	}
}
