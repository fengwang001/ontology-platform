package journal_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ontology/audit"
	"ontology/change"
	"ontology/journal"
	"ontology/view"
)

const (
	truncRecs  = 200
	bodyLen    = 22 + 6 + 8 // 固定部分 + len("gNNNNN") + len("idNNNNNN")
	recLen     = 4 + bodyLen + 4
	truncTotal = journal.HeaderLen + truncRecs*recLen
)

func buildLog(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "full.jrn")
	w, err := journal.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < truncRecs; i++ {
		c := change.Change{Version: uint64(i + 1), Op: change.Insert,
			HasGroup: true, Group: fmt.Sprintf("g%05d", i),
			ID: fmt.Sprintf("id%06d", i), Value: float64(i)}
		if err := w.Log(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// wantClass 由截断点推导期望分类；ok=false 表示该截断点是干净前缀。
func wantClass(cut int) (journal.Class, bool) {
	if cut < journal.HeaderLen {
		return journal.ClassHeaderShort, true
	}
	switch slot := (cut - journal.HeaderLen) % recLen; {
	case slot == 0:
		return 0, false
	case slot < 4:
		return journal.ClassLenShort, true
	case slot < 4+bodyLen:
		return journal.ClassBodyShort, true
	default:
		return journal.ClassCRC, true
	}
}

// TestTruncation 逐字节截断 1..len-1，逐点断言分类与生效记录数。
func TestTruncation(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(buildLog(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != truncTotal {
		t.Fatalf("文件长度 %d, 期望 %d", len(data), truncTotal)
	}
	seen := map[journal.Class]int{}
	for cut := 1; cut < len(data); cut++ {
		p := filepath.Join(dir, "cut.jrn")
		if err := os.WriteFile(p, data[:cut], 0o644); err != nil {
			t.Fatal(err)
		}
		recs, valid, err := journal.Replay(p)
		wantRecs := 0
		if cut >= journal.HeaderLen {
			wantRecs = (cut - journal.HeaderLen) / recLen
		}
		if len(recs) != wantRecs {
			t.Fatalf("cut=%d 生效记录 %d, 期望 %d", cut, len(recs), wantRecs)
		}
		if cut >= journal.HeaderLen && valid != journal.HeaderLen+wantRecs*recLen {
			t.Fatalf("cut=%d 有效字节 %d", cut, valid)
		}
		cls, isErr := wantClass(cut)
		if !isErr {
			if err != nil {
				t.Fatalf("cut=%d 干净前缀却报错 %v", cut, err)
			}
			continue
		}
		var je *journal.Error
		if !errors.As(err, &je) || je.Class != cls {
			t.Fatalf("cut=%d 分类 %v, 期望 %v", cut, err, cls)
		}
		seen[cls]++
	}
	for _, cls := range []journal.Class{journal.ClassHeaderShort, journal.ClassLenShort,
		journal.ClassBodyShort, journal.ClassCRC} {
		if seen[cls] == 0 {
			t.Fatalf("分类 %v 未被任何截断点触发", cls)
		}
	}
}

// TestCorruption 表驱动：干净重放 / CRC 字节被翻转。
func TestCorruption(t *testing.T) {
	dir := t.TempDir()
	data, _ := os.ReadFile(buildLog(t, dir))
	crcFlip := append([]byte(nil), data...)
	crcFlip[journal.HeaderLen+4+bodyLen] ^= 0xFF
	cases := []struct {
		name  string
		data  []byte
		class journal.Class
		isErr bool
		recs  int
	}{
		{"clean", data, 0, false, truncRecs},
		{"crc-flip", crcFlip, journal.ClassCRC, true, 0},
	}
	for _, tc := range cases {
		p := filepath.Join(dir, tc.name+".jrn")
		os.WriteFile(p, tc.data, 0o644)
		recs, _, err := journal.Replay(p)
		if len(recs) != tc.recs {
			t.Errorf("%s 生效记录 %d, 期望 %d", tc.name, len(recs), tc.recs)
		}
		var je *journal.Error
		if tc.isErr && (!errors.As(err, &je) || je.Class != tc.class) {
			t.Errorf("%s 错误 %v, 期望分类 %v", tc.name, err, tc.class)
		}
		if !tc.isErr && err != nil {
			t.Errorf("%s 意外错误 %v", tc.name, err)
		}
	}
}

// TestTruncatedReplay 逐字节截断的日志恢复后，视图等于完整前缀的全量重算。
func TestTruncatedReplay(t *testing.T) {
	dir := t.TempDir()
	src := buildLog(t, dir)
	data, _ := os.ReadFile(src)
	var all []change.Change
	for i := 0; i < truncRecs; i++ {
		c := change.Change{Version: uint64(i + 1), Op: change.Insert, HasGroup: true,
			Group: fmt.Sprintf("g%05d", i), ID: fmt.Sprintf("id%06d", i), Value: float64(i)}
		all = append(all, c)
	}
	for cut := 1; cut < len(data); cut++ {
		p := filepath.Join(dir, "c.jrn")
		os.WriteFile(p, data[:cut], 0o644)
		got, err := view.Open(p)
		if err != nil {
			t.Fatalf("cut=%d: %v", cut, err)
		}
		n := 0
		if cut >= journal.HeaderLen {
			n = (cut - journal.HeaderLen) / recLen
		}
		model := map[string]audit.Record{}
		for _, c := range all[:n] {
			model[c.ID] = audit.Record{Group: c.Group, Value: c.Value}
		}
		if err := audit.Compare(got.Snapshot(), audit.Full(model)); err != nil {
			t.Fatalf("cut=%d: %v", cut, err)
		}
		got.Close()
	}
}
