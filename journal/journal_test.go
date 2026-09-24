package journal_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/change"
	"ontology/journal"
)

const nRec = 200

func mkChange(i int) change.Change {
	return change.Change{
		Ver: uint64(i + 1), Op: change.Insert, RecID: uint64(i + 1),
		Group: "g", Value: float64(i + 1),
	}
}

func buildLog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	j, err := journal.Create(dir, nRec)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < nRec; i++ {
		if err := j.Append(mkChange(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "view.journal")
}

func classify(t *testing.T, path string, cut int) error {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p2 := filepath.Join(t.TempDir(), "v.journal")
	if err := os.WriteFile(p2, data[:cut], 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err = journal.Replay(p2, nRec)
	return err
}

func TestTruncationClassified(t *testing.T) {
	src := buildLog(t)
	frame := 1 + change.BodyLen + 4 // varint len + body + crc
	fullLen := 13 + nRec*frame
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != fullLen {
		t.Fatalf("file len=%d want %d", len(data), fullLen)
	}
	seen := map[error]int{}
	for cut := 1; cut < fullLen; cut++ {
		err := classify(t, src, cut)
		var want error
		switch {
		case cut < 13:
			want = journal.ErrShortHeader
		case (cut-13)%frame == 0:
			want = journal.ErrShortLength
		case (cut-13)%frame <= 1+change.BodyLen:
			want = journal.ErrShortRecord
		default:
			want = journal.ErrShortRecord // CRC bytes missing => incomplete
		}
		if !errors.Is(err, want) {
			t.Fatalf("cut=%d got %v want %v", cut, err, want)
		}
		seen[want]++
	}
	if seen[journal.ErrShortHeader] != 12 {
		t.Fatalf("header cuts=%d", seen[journal.ErrShortHeader])
	}
	if seen[journal.ErrShortLength] != nRec-1 {
		t.Fatalf("length cuts=%d", seen[journal.ErrShortLength])
	}
	if seen[journal.ErrShortRecord] == 0 {
		t.Fatal("no body/crc cuts observed")
	}
}

func TestTruncatedPrefixApplies(t *testing.T) {
	src := buildLog(t)
	frame := 1 + change.BodyLen + 4
	cut := 13 + 50*frame + 50 // inside record 51's body
	dir := t.TempDir()
	p := filepath.Join(dir, "view.journal")
	data, _ := os.ReadFile(src)
	if err := os.WriteFile(p, data[:cut], 0o644); err != nil {
		t.Fatal(err)
	}
	recs, n, err := journal.Replay(p, -1)
	if err != nil {
		t.Fatalf("lenient replay: %v", err)
	}
	if n != 50 {
		t.Fatalf("replayed %d records want 50", n)
	}
	if recs[49].RecID != 50 {
		t.Fatalf("last rec id=%d", recs[49].RecID)
	}
}

func TestCRCMismatch(t *testing.T) {
	src := buildLog(t)
	data, _ := os.ReadFile(src)
	frame := 1 + change.BodyLen + 4
	cut := 13 + 50*frame + frame // one complete extra frame
	data[cut-1] ^= 0xFF
	dir := t.TempDir()
	p := filepath.Join(dir, "v.journal")
	if err := os.WriteFile(p, data[:cut], 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := journal.Replay(p, 51)
	if !errors.Is(err, journal.ErrCRC) {
		t.Fatalf("got %v want ErrCRC", err)
	}
}
