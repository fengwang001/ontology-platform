package frame

import (
	"bytes"
	"testing"
)

func runFrame(t *testing.T, wire []byte, chunk int, maxLine int) ([]byte, uint64, int, error) {
	t.Helper()
	r := NewReader(maxLine)
	var body bytes.Buffer
	r.Sink = func(b byte) error { body.WriteByte(b); return nil }
	consumed := 0
	var err error
	for consumed < len(wire) {
		if r.Done() {
			break
		}
		end := consumed + chunk
		if end > len(wire) {
			end = len(wire)
		}
		var n int
		n, err = r.Feed(wire[consumed:end])
		consumed += n
		if err != nil {
			return body.Bytes(), r.Size(), consumed, err
		}
		if r.Done() {
			break
		}
	}
	if err == nil && !r.Done() {
		err = r.Close()
	}
	return body.Bytes(), r.Size(), consumed, err
}

func TestFrameAllSplits(t *testing.T) {
	wire := []byte("5;foo=\"a;b\"\r\nhello\r\n")
	want := []byte("hello")
	for chunk := 1; chunk <= len(wire); chunk++ {
		body, size, n, err := runFrame(t, wire, chunk, 0)
		if err != nil {
			t.Fatalf("chunk %d: %v", chunk, err)
		}
		if size != 5 || !bytes.Equal(body, want) || n != len(wire) {
			t.Fatalf("chunk %d: size=%d body=%q consumed=%d", chunk, size, body, n)
		}
	}
}

func TestFrameZeroSize(t *testing.T) {
	wire := []byte("0\r\n") // a zero-size chunk is just the size line
	for chunk := 1; chunk <= len(wire); chunk++ {
		body, size, n, err := runFrame(t, wire, chunk, 0)
		if err != nil || len(body) != 0 || n != len(wire) {
			t.Fatalf("chunk %d: body=%q consumed=%d err=%v", chunk, body, n, err)
		}
		if size != 0 {
			t.Fatalf("chunk %d: size=%d want 0", chunk, size)
		}
	}
}

func TestFrameCRLFErrors(t *testing.T) {
	base := "4\r\n" + "Wiki"
	cases := []struct {
		name string
		wire string
		off  int
	}{
		{"lf instead of crlf", base + "\n", 7},
		{"extra byte", base + "X\r\n", 7},
		{"missing", base, 7},
		{"half crlf", base + "\r", 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for chunk := 1; chunk <= len(tc.wire); chunk++ {
				_, _, _, err := runFrame(t, []byte(tc.wire), chunk, 0)
				fe, ok := AsError(err)
				if !ok || fe.Kind != KindMissingCRLF || fe.Offset != tc.off {
					t.Fatalf("chunk %d: got %v, want MissingCRLF @%d", chunk, err, tc.off)
				}
			}
		})
	}
}

func TestFrameHeaderError(t *testing.T) {
	wire := []byte("x\r\n")
	for chunk := 1; chunk <= len(wire); chunk++ {
		_, _, _, err := runFrame(t, wire, chunk, 0)
		fe, ok := AsError(err)
		if !ok || fe.Kind != KindNonHex || fe.Offset != 0 {
			t.Fatalf("chunk %d: got %v", chunk, err)
		}
	}
}
