package multipart

import (
	"bytes"
	"strings"
	"testing"

	"ontology/coalesce"
)

func TestNewBoundaryFormat(t *testing.T) {
	b, err := NewBoundary(bytes.NewReader(make([]byte, 64)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b, "ontology-") || len(b) != len("ontology-")+32 {
		t.Fatalf("unexpected boundary %q", b)
	}
	// Deterministic reader -> deterministic token (hex of zero bytes).
	if b != "ontology-00000000000000000000000000000000" {
		t.Fatalf("boundary = %q", b)
	}
}

func TestNewBoundaryRandomnessError(t *testing.T) {
	if _, err := NewBoundary(strings.NewReader("too short")); err == nil {
		t.Fatal("expected error from short random source")
	}
}

func TestConflicts(t *testing.T) {
	payload := []byte("xx--ontology-deadbeef yy")
	if !Conflicts("ontology-deadbeef", payload) {
		t.Fatal("bare token inside payload must conflict")
	}
	if Conflicts("ontology-cafe", payload) {
		t.Fatal("absent token must not conflict")
	}
}

func TestBuildFraming(t *testing.T) {
	rs := []coalesce.Range{{From: 0, To: 1}, {From: 4, To: 5}}
	payload := []byte("ABEF") // contents of 0-1 then 4-5
	body := Build("B", rs, payload, 10, "application/octet-stream")
	want := "--B\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Range: bytes 0-1/10\r\n" +
		"\r\nAB\r\n" +
		"--B\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Range: bytes 4-5/10\r\n" +
		"\r\nEF\r\n" +
		"--B--\r\n"
	if string(body) != want {
		t.Fatalf("body =\n%q\nwant\n%q", body, want)
	}
	// The closing delimiter appears exactly once, at the end.
	if bytes.Count(body, []byte("--B--")) != 1 || !bytes.HasSuffix(body, []byte("--B--\r\n")) {
		t.Fatal("closing delimiter malformed")
	}
}
