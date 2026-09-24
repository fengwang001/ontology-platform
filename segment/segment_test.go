package segment

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func writeSeg(t *testing.T, dir string, n int, first uint64, nEvents int) string {
	t.Helper()
	path := filepath.Join(dir, "seg.log")
	w, err := Create(path, first)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < nEvents; i++ {
		if err := w.Append([]byte{byte(i), byte(i >> 8), byte(n)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := writeSeg(t, dir, 7, 100, 500)
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.h.FirstSeq != 100 || r.h.Count != 500 {
		t.Fatalf("header = %+v", r.h)
	}
	var prevOff int64 = HeaderSize
	for i := 0; i < 500; i++ {
		off, body, err := r.Next()
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if off < prevOff {
			t.Fatalf("offset went backwards: %d < %d", off, prevOff)
		}
		if len(body) != 3 || body[0] != byte(i) {
			t.Fatalf("event %d body = %v", i, body)
		}
		prevOff = off + int64(4+len(body)+4)
	}
	if _, _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestEmptyPayload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seg.log")
	w, _ := Create(path, 0)
	if err := w.Append(nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	off, body, err := r.Next()
	if err != nil || len(body) != 0 || off != HeaderSize {
		t.Fatalf("off=%d body=%v err=%v", off, body, err)
	}
}

func TestByteTruncationClassification(t *testing.T) {
	dir := t.TempDir()
	path := writeSeg(t, dir, 1, 0, 500)
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	frameEnds := scanFrameEnds(t, full)

	want := func(size int) error {
		if size < HeaderSize {
			return ErrTruncatedHeader
		}
		for k, end := range frameEnds {
			if size == end {
				return nil // 恰好整帧边界：帧流完整，count 虚高
			}
			nextStart := end
			nextEnd := frameEnds[k+1]
			if size > nextStart && size < nextEnd {
				fl := int(uint32(full[nextStart])<<24 | uint32(full[nextStart+1])<<16 |
					uint32(full[nextStart+2])<<8 | uint32(full[nextStart+3]))
				switch {
				case size < nextStart+4:
					return ErrTruncatedLength
				case size < nextStart+4+fl:
					return ErrTruncatedBody
				default:
					return ErrTruncatedCRC
				}
			}
		}
		return nil
	}

	counts := map[error]int{}
	for size := 1; size < len(full); size++ {
		tpath := filepath.Join(dir, "cut.log")
		if err := os.WriteFile(tpath, full[:size], 0o644); err != nil {
			t.Fatal(err)
		}
		rep, err := Inspect(tpath)
		w := want(size)
		switch {
		case w == nil:
			if err != nil || !errors.Is(rep.Err, ErrCountMismatch) {
				t.Fatalf("size=%d: want count mismatch, rep=%+v err=%v", size, rep, err)
			}
			counts[ErrCountMismatch]++
		default:
			if !errors.Is(rep.Err, w) {
				t.Fatalf("size=%d: want %v, got %v", size, w, rep.Err)
			}
			counts[w]++
		}
	}
	for _, e := range []error{ErrTruncatedHeader, ErrTruncatedLength, ErrTruncatedBody, ErrTruncatedCRC, ErrCountMismatch} {
		if counts[e] == 0 {
			t.Fatalf("error class never observed: %v (counts=%v)", e, counts)
		}
	}
}

func scanFrameEnds(t *testing.T, full []byte) []int {
	t.Helper()
	ends := []int{HeaderSize}
	off := HeaderSize
	for off < len(full) {
		fl := int(uint32(full[off])<<24 | uint32(full[off+1])<<16 |
			uint32(full[off+2])<<8 | uint32(full[off+3]))
		off += 4 + fl + 4
		ends = append(ends, off)
	}
	return ends
}

func TestCRCMismatch(t *testing.T) {
	dir := t.TempDir()
	path := writeSeg(t, dir, 1, 0, 10)
	b, _ := os.ReadFile(path)
	b[HeaderSize+5] ^= 0xFF
	bad := filepath.Join(dir, "bad.log")
	if err := os.WriteFile(bad, b, 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := Inspect(bad)
	if !errors.Is(rep.Err, ErrCRCMismatch) || rep.Frames != 0 || rep.ValidEnd != HeaderSize {
		t.Fatalf("rep = %+v", rep)
	}
}
