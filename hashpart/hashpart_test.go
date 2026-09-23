package hashpart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPartition(t *testing.T) {
	const parts = 8
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		p := Partition(key, parts)
		if p < 0 || p >= parts {
			t.Fatalf("分区越界: %d", p)
		}
		if Partition(key, parts) != p {
			t.Fatalf("同一键分区不稳定")
		}
	}
}

func writeSeg(t *testing.T, dir string, part, nPayloads int) (string, [][]byte) {
	t.Helper()
	s, err := NewSpiller(dir, 4)
	if err != nil {
		t.Fatal(err)
	}
	var payloads [][]byte
	for i := 0; i < nPayloads; i++ {
		payloads = append(payloads, []byte(fmt.Sprintf("payload-%04d", i)))
	}
	if err := s.WriteSegment(part, payloads); err != nil {
		t.Fatal(err)
	}
	return s.Segments(part)[0], payloads
}

func TestWriteReadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path, want := writeSeg(t, dir, 2, 10)
	f, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Part != 2 || f.Seg != 0 || len(f.Payloads) != len(want) {
		t.Fatalf("头部不符: %+v", f)
	}
	for i := range want {
		if string(f.Payloads[i]) != string(want[i]) {
			t.Fatalf("第%d条不符", i)
		}
	}
}

// TestTruncateClassify 对 400 行分区文件逐字节截断，每个截断点都要被正确分类。
func TestTruncateClassify(t *testing.T) {
	dir := t.TempDir()
	path, _ := writeSeg(t, dir, 0, 400)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// 预计算每条记录的四段字节区间。
	type span struct{ start, body, crc, end int }
	var spans []span
	for off := HeaderLen; off < len(data); {
		n := int(data[off]) | int(data[off+1])<<8 | int(data[off+2])<<16 | int(data[off+3])<<24
		spans = append(spans, span{off, off + 4, off + 4 + n, off + 8 + n})
		off += 8 + n
	}
	classify := func(tcut int) error {
		if tcut < HeaderLen {
			return ErrHeader
		}
		for _, sp := range spans {
			switch {
			case tcut >= sp.start && tcut < sp.body:
				return ErrLengthPrefix
			case tcut >= sp.body && tcut < sp.crc:
				return ErrBody
			case tcut >= sp.crc && tcut < sp.end:
				return ErrCRC
			}
		}
		return ErrLengthPrefix // 齐文件尾之前的记录边界：期望记录数未到
	}
	counts := map[error]int{ErrHeader: 0, ErrLengthPrefix: 0, ErrBody: 0, ErrCRC: 0}
	for tcut := 1; tcut < len(data); tcut++ {
		fpath := filepath.Join(dir, "part-0000-seg-000000")
		if err := os.WriteFile(fpath, data[:tcut], 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := ReadFile(fpath)
		want := classify(tcut)
		if !errors.Is(err, want) {
			t.Fatalf("截断到 %d: 期望 %v, 得到 %v", tcut, want, err)
		}
		var fe *FileError
		if !errors.As(err, &fe) || fe.Part != 0 || fe.Offset < 0 || fe.Offset >= int64(len(data)) {
			t.Fatalf("截断到 %d: 错误未携带分区/偏移: %v", tcut, err)
		}
		counts[want]++
	}
	for _, sentinel := range []error{ErrHeader, ErrLengthPrefix, ErrBody, ErrCRC} {
		if counts[sentinel] == 0 {
			t.Fatalf("分类 %v 未被覆盖", sentinel)
		}
	}
	t.Logf("截断分类计数: %v", counts)
}

func TestCorruptCRC(t *testing.T) {
	dir := t.TempDir()
	path, _ := writeSeg(t, dir, 1, 5)
	data, _ := os.ReadFile(path)
	data[HeaderLen+6] ^= 0xFF // 翻转第一条记录行体内一字节
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(path); !errors.Is(err, ErrCRC) {
		t.Fatalf("期望 ErrCRC, 得到 %v", err)
	}
}

func TestTmpCleanupOnStartup(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "part-0003-seg-000007"+tmpSuffix)
	if err := os.WriteFile(junk, []byte("HAG"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSpiller(dir, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(junk); !os.IsNotExist(err) {
		t.Fatalf("半截临时文件未被清理")
	}
	if got := s.Segments(3); len(got) != 0 {
		t.Fatalf("半截临时文件被当成有效分区: %v", got)
	}
}

func TestWriteFailureAndCleanup(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSpiller(dir, 4)
	if err != nil {
		t.Fatal(err)
	}
	s.SetFailOnWrite(2)
	if err := s.WriteSegment(0, [][]byte{[]byte("a")}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSegment(1, [][]byte{[]byte("b")}); !errors.Is(err, ErrWrite) {
		t.Fatalf("期望 ErrWrite, 得到 %v", err)
	}
	if s.Writes() != 2 {
		t.Fatalf("写计数错误: %d", s.Writes())
	}
	s.Cleanup()
	if got := s.Remaining(); got != 0 {
		t.Fatalf("清理后残留 %d 个文件", got)
	}
}
