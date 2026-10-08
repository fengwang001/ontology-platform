package ontology

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEntries(t *testing.T, path string, n int) (offsets []int64) {
	t.Helper()
	w, err := openLogWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.close()
	for i := 1; i <= n; i++ {
		off, err := w.offset()
		if err != nil {
			t.Fatal(err)
		}
		offsets = append(offsets, off)
		entry := &LogEntry{Seq: uint64(i), Op: Operation{
			Kind:       OpDeclareObjectType,
			ObjectType: ObjectTypeID("T" + string(rune('A'+i-1))),
		}}
		if err := w.append(entry); err != nil {
			t.Fatal(err)
		}
	}
	end, err := w.offset()
	if err != nil {
		t.Fatal(err)
	}
	offsets = append(offsets, end)
	return offsets
}

func truncateFile(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
}

// 撕裂尾部边界：最后一条日志项在任意字节处被截断，都必须被整体丢弃，
// 重放结果与「从未写入该条」完全一致。
func TestReadLogTornTailEveryByte(t *testing.T) {
	dir := t.TempDir()
	const total = 5
	// 先写一份完整日志，记录每条帧的起始偏移。
	src := filepath.Join(dir, "full.log")
	offsets := writeEntries(t, src, total)
	full, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	lastStart := offsets[total-1]
	lastEnd := offsets[total]

	// 在最后一条记录内部的每一个可能截断点（含恰好截在帧边界上）验证。
	for cut := lastStart; cut < lastEnd; cut++ {
		path := filepath.Join(dir, "torn.log")
		if err := os.WriteFile(path, full[:cut], 0o644); err != nil {
			t.Fatal(err)
		}
		entries, validBytes, err := readLog(path)
		if err != nil {
			t.Fatalf("cut=%d: %v", cut, err)
		}
		if len(entries) != total-1 {
			t.Fatalf("cut=%d: got %d entries, want %d", cut, len(entries), total-1)
		}
		if validBytes != lastStart {
			t.Fatalf("cut=%d: validBytes=%d, want %d", cut, validBytes, lastStart)
		}
		for i, e := range entries {
			if e.Seq != uint64(i+1) {
				t.Fatalf("cut=%d: entry %d has seq %d", cut, i, e.Seq)
			}
		}
	}
}

// 在任意两条日志项之间的截断：重放必须精确得到前 k 条。
func TestReadLogAtEveryBoundary(t *testing.T) {
	dir := t.TempDir()
	const total = 6
	src := filepath.Join(dir, "full.log")
	offsets := writeEntries(t, src, total)
	full, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	for k := 0; k <= total; k++ {
		path := filepath.Join(dir, "cut.log")
		if err := os.WriteFile(path, full[:offsets[k]], 0o644); err != nil {
			t.Fatal(err)
		}
		entries, validBytes, err := readLog(path)
		if err != nil {
			t.Fatalf("k=%d: %v", k, err)
		}
		if len(entries) != k {
			t.Fatalf("k=%d: got %d entries", k, len(entries))
		}
		if validBytes != offsets[k] {
			t.Fatalf("k=%d: validBytes=%d, want %d", k, validBytes, offsets[k])
		}
	}
}

// CRC 损坏的记录同样被整体丢弃。
func TestReadLogCorruptCRC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.log")
	offsets := writeEntries(t, path, 3)
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	// 翻转第 3 条记录载荷的一个字节。
	if _, err := f.WriteAt([]byte{0xFF}, offsets[2]+frameHeaderSize); err != nil {
		t.Fatal(err)
	}
	f.Close()
	entries, validBytes, err := readLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if validBytes != offsets[2] {
		t.Fatalf("validBytes=%d, want %d", validBytes, offsets[2])
	}
}

// 序号不连续（空洞或重复）视为损坏，报错而不是默默跳过。
func TestReadLogSeqGap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gap.log")
	w, err := openLogWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.close()
	w.append(&LogEntry{Seq: 1, Op: Operation{Kind: OpDeclareObjectType, ObjectType: "A"}})
	w.append(&LogEntry{Seq: 3, Op: Operation{Kind: OpDeclareObjectType, ObjectType: "B"}})
	if _, _, err := readLog(path); err == nil {
		t.Fatal("expected seq gap error")
	}
}

func TestReadLogMissingFile(t *testing.T) {
	entries, validBytes, err := readLog(filepath.Join(t.TempDir(), "none.log"))
	if err != nil || len(entries) != 0 || validBytes != 0 {
		t.Fatalf("got %v %d %v", entries, validBytes, err)
	}
}
