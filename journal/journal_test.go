package journal_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/change"
	"ontology/journal"
)

func strptr(s string) *string { return &s }

// 构造 n 条等长 payload 的日志字节（分组键 "g1"，payload 30 字节，记录步长 38）。
func buildLog(t *testing.T, n int) ([]byte, [][]byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.log")
	j, err := journal.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	var payloads [][]byte
	for i := 0; i < n; i++ {
		p := change.Change{Version: uint64(i + 1), Op: change.OpInsert, ID: uint64(i + 1), Group: strptr("g1"), Value: float64(i)}.Encode()
		if err := j.Append(p); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, p)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data, payloads
}

func TestAppendReplayRoundtrip(t *testing.T) {
	_, payloads := buildLog(t, 200)
	if len(payloads) != 200 {
		t.Fatalf("got %d payloads", len(payloads))
	}
	for i, p := range payloads {
		c, err := change.Decode(p)
		if err != nil || c.ID != uint64(i+1) || c.Version != uint64(i+1) {
			t.Fatalf("record %d decode: %+v err=%v", i, c, err)
		}
	}
}

// 逐字节截断：每个截断点都必须被分类为四类之一（或恰在记录边界为合法），
// 且重放出的完整记录条数等于截断点之前的完整记录数。
func TestTruncationClassification(t *testing.T) {
	data, payloads := buildLog(t, 200)
	payload := len(payloads[0])
	recSize := payload + 8
	if len(data) != journal.HeaderSize+200*recSize {
		t.Fatalf("log size %d", len(data))
	}
	classOf := func(cut int) (journal.Class, int) {
		if cut < journal.HeaderSize {
			return journal.ClassHeader, 0
		}
		r := (cut - journal.HeaderSize) % recSize
		done := (cut - journal.HeaderSize) / recSize
		switch {
		case r == 0:
			return journal.ClassOK, done
		case r < 4:
			return journal.ClassLength, done
		case r < 4+payload:
			return journal.ClassRecord, done
		default:
			return journal.ClassCRC, done
		}
	}
	errFor := map[journal.Class]error{
		journal.ClassOK: nil, journal.ClassHeader: journal.ErrHeaderIncomplete,
		journal.ClassLength: journal.ErrLengthIncomplete, journal.ClassRecord: journal.ErrRecordIncomplete,
		journal.ClassCRC: journal.ErrCRCMismatch,
	}
	seen := map[journal.Class]int{}
	for cut := 1; cut < len(data); cut++ {
		recs, cls, err := journal.ReplayBytes(data[:cut])
		wantCls, wantN := classOf(cut)
		if cls != wantCls || len(recs) != wantN || !errors.Is(err, errFor[cls]) {
			t.Fatalf("cut=%d: got cls=%v n=%d err=%v, want cls=%v n=%d", cut, cls, len(recs), err, wantCls, wantN)
		}
		seen[cls]++
	}
	for _, c := range []journal.Class{journal.ClassHeader, journal.ClassLength, journal.ClassRecord, journal.ClassCRC, journal.ClassOK} {
		if seen[c] == 0 {
			t.Fatalf("class %v never observed", c)
		}
	}
}
