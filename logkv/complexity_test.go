package logkv

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"
)

// buildHintedSegment 构建含 K 个不同键、N 条记录的已封口段，
// 返回段文件与提示文件内容。
func buildHintedSegment(t *testing.T, keys, records int) (seg, hint []byte) {
	t.Helper()
	s := newTestStore(t, 1<<20)
	for i := 0; i < records; i++ {
		mustPut(t, s, fmt.Sprintf("key-%03d", i%keys), fmt.Sprintf("value-%d", i))
	}
	rotateNow(t, s)
	seg = readFile(t, segPath(s, 1))
	hint = readFile(t, filepath.Join(s.dir, "000001.hint"))
	s.Close()
	return seg, hint
}

// TestHintRecoveryCostScalesWithDistinctKeys 证明：使用有效提示信息
// 恢复一个已封口段的开销只随不同键数增长，不随记录总数与字节数增长。
//
// 验证方式：
//  1. 相同不同键数、记录数相差 100 倍的两个段，提示文件字节数完全相同；
//  2. 把段内容整体置零（长度不变）后恢复仍然成功且采用提示信息，
//     说明恢复根本不读取记录字节，开销与段字节数无关。
func TestHintRecoveryCostScalesWithDistinctKeys(t *testing.T) {
	segSmall, hintSmall := buildHintedSegment(t, 8, 20)
	segBig, hintBig := buildHintedSegment(t, 8, 2000)
	if len(hintSmall) != len(hintBig) {
		t.Fatalf("hint size %d vs %d with same distinct keys", len(hintSmall), len(hintBig))
	}
	if len(segBig) <= 10*len(segSmall) {
		t.Fatalf("segment sizes %d vs %d, expected big difference", len(segBig), len(segSmall))
	}
	t.Logf("distinct keys=8: records=20 seg=%dB hint=%dB; records=2000 seg=%dB hint=%dB",
		len(segSmall), len(hintSmall), len(segBig), len(hintBig))

	// 段内容整体置零（长度不变），恢复仍采用提示信息且不读记录。
	zeroed := make([]byte, len(segBig))
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "000001.seg"), zeroed)
	writeFile(t, filepath.Join(dir, "000001.hint"), hintBig)
	writeFile(t, filepath.Join(dir, "000002.seg"), nil) // 空活动段
	s, err := Open(Config{Dir: dir, MaxSegmentBytes: 1 << 20, WriteHints: true})
	if err != nil {
		t.Fatalf("open with zeroed segment: %v", err)
	}
	defer s.Close()
	st := s.Stats()
	if st.HintSegments != 1 || st.ScannedSegments != 0 {
		t.Fatalf("hint=%d scanned=%d, want hint-only recovery", st.HintSegments, st.ScannedSegments)
	}
	// 目录中有全部 8 个键（来自提示信息）。
	for i := 0; i < 8; i++ {
		_, status, err := s.Get([]byte(fmt.Sprintf("key-%03d", i)))
		if err == nil {
			t.Fatalf("get on zeroed segment should report corruption")
		}
		if !IsKind(err, KindSegmentCorruption) || status != StatusNotFound {
			t.Fatalf("get: %v", err)
		}
	}
}

// TestReadCostIndependentOfTotals 证明：一次读取的开销不随总键数
// 与总段数增长——每次命中读取恰好访问盘上记录一次，未命中不访问。
func TestReadCostIndependentOfTotals(t *testing.T) {
	s := newTestStore(t, 1<<20)
	defer s.Close()
	const totalKeys = 500
	const totalSegs = 20
	keysPerSeg := totalKeys / totalSegs
	for seg := 0; seg < totalSegs; seg++ {
		for i := 0; i < keysPerSeg; i++ {
			mustPut(t, s, fmt.Sprintf("key-%04d", seg*keysPerSeg+i), "v")
		}
		rotateNow(t, s)
	}
	mustPut(t, s, "fresh", "v")
	sealed, _ := segIDs(t, s)
	if len(sealed) != totalSegs {
		t.Fatalf("sealed=%d want %d", len(sealed), totalSegs)
	}
	base := s.Stats().RecordReads
	// 命中读取：最老段、中间段、活动段各取若干键。
	probe := []string{"key-0000", "key-0010", "key-0250", "key-0499", "fresh"}
	for _, k := range probe {
		before := s.Stats().RecordReads
		mustGet(t, s, k, "v")
		if delta := s.Stats().RecordReads - before; delta != 1 {
			t.Fatalf("get %q: record reads delta=%d want 1", k, delta)
		}
	}
	// 未命中读取：不访问盘上记录。
	for _, k := range []string{"nope-1", "nope-2"} {
		before := s.Stats().RecordReads
		mustStatus(t, s, k, StatusNotFound)
		if delta := s.Stats().RecordReads - before; delta != 0 {
			t.Fatalf("get %q: record reads delta=%d want 0", k, delta)
		}
	}
	total := s.Stats().RecordReads - base
	t.Logf("keys=%d segments=%d probes=%d record-reads=%d (== 每命中 1 次)",
		totalKeys, totalSegs+1, len(probe)+2, total)
	if total != uint64(len(probe)) {
		t.Fatalf("total record reads=%d want %d", total, len(probe))
	}
}

// TestHintContentSanity 提示信息不含值：文件内容中找不到任何值字节串。
func TestHintContentSanity(t *testing.T) {
	s := newTestStore(t, 1<<20)
	mustPut(t, s, "alpha", "secret-value-one")
	mustPut(t, s, "beta", "secret-value-two")
	rotateNow(t, s)
	hint := readFile(t, filepath.Join(s.dir, "000001.hint"))
	s.Close()
	if bytes.Contains(hint, []byte("secret-value")) {
		t.Fatalf("hint must not contain values")
	}
	if !bytes.Contains(hint, []byte("alpha")) {
		t.Fatalf("hint should contain keys")
	}
}
