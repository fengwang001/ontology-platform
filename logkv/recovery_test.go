package logkv

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// op 是一条用于构建预期模型的写入操作。
type op struct {
	key, val  string
	tombstone bool
}

// expectedFrom 按“写序号最大者胜”对完整记录序列求值。
func expectedFrom(ops []op) map[string]op {
	m := make(map[string]op)
	for _, o := range ops {
		m[o.key] = o
	}
	return m
}

func recordLen(o op) int {
	return RecordSize(len(o.key), len(o.val))
}

// TestTruncateActiveEveryByte 在活动段的每个字节边界截断后恢复：
// 撕裂尾部被截断到最后一条完整记录之后，截断字节数可查询，
// 全部完整记录保持可读。
func TestTruncateActiveEveryByte(t *testing.T) {
	ops := []op{
		{key: "alpha", val: "1"},
		{key: "beta", val: "22"},
		{key: "alpha", tombstone: true},
		{key: "gamma", val: "333"},
		{key: "beta", val: "4444"},
	}
	src := newTestStore(t, 1<<20)
	for _, o := range ops {
		if o.tombstone {
			mustDelete(t, src, o.key)
		} else {
			mustPut(t, src, o.key, o.val)
		}
	}
	full := readFile(t, segPath(src, 1))
	src.Close()

	// 记录边界。
	var bounds []int
	off := 0
	for _, o := range ops {
		off += recordLen(o)
		bounds = append(bounds, off)
	}
	if bounds[len(bounds)-1] != len(full) {
		t.Fatalf("record sizes %v != file size %d", bounds, len(full))
	}

	for n := 0; n <= len(full); n++ {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "000001.seg"), full[:n])
		s, err := Open(Config{Dir: dir, MaxSegmentBytes: 1 << 20, WriteHints: true})
		if err != nil {
			t.Fatalf("prefix %d: open: %v", n, err)
		}
		// 完整记录数。
		complete := sort.SearchInts(bounds, n+1)
		validEnd := 0
		if complete > 0 {
			validEnd = bounds[complete-1]
		}
		if got := s.Stats().TruncatedBytes; got != uint64(n-validEnd) {
			t.Fatalf("prefix %d: truncated=%d want %d", n, got, n-validEnd)
		}
		if fi, _ := os.Stat(filepath.Join(dir, "000001.seg")); fi.Size() != int64(validEnd) {
			t.Fatalf("prefix %d: file size=%d want %d", n, fi.Size(), validEnd)
		}
		want := expectedFrom(ops[:complete])
		for _, o := range ops {
			w, ok := want[o.key]
			switch {
			case !ok:
				mustStatus(t, s, o.key, StatusNotFound)
			case w.tombstone:
				mustStatus(t, s, o.key, StatusDeleted)
			default:
				mustGet(t, s, o.key, w.val)
			}
		}
		// 恢复后仍可继续写入。
		mustPut(t, s, "after", "x")
		mustGet(t, s, "after", "x")
		s.Close()
	}
}

// flipBit 返回翻转第 i 位后的副本。
func flipBit(b []byte, i int) []byte {
	out := append([]byte(nil), b...)
	out[i/8] ^= 1 << (uint(i) % 8)
	return out
}

// recordOffsets 返回每条记录的起始偏移。
func recordOffsets(ops []op) []int {
	var offs []int
	off := 0
	for _, o := range ops {
		offs = append(offs, off)
		off += recordLen(o)
	}
	return offs
}

// TestSealedSegmentBitFlipNoHint 已封口段在每个位置翻转一位：
// 无提示信息时恢复整体失败，报告段号与首个损坏位置。
func TestSealedSegmentBitFlipNoHint(t *testing.T) {
	ops1 := []op{{key: "k1", val: "v1"}, {key: "k2", val: "v2"}, {key: "k3", tombstone: true}}
	src, err := Open(Config{Dir: t.TempDir(), MaxSegmentBytes: 1 << 20, WriteHints: false})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops1 {
		if o.tombstone {
			mustDelete(t, src, o.key)
		} else {
			mustPut(t, src, o.key, o.val)
		}
	}
	rotateNow(t, src)
	mustPut(t, src, "active-key", "av")
	seg1 := readFile(t, segPath(src, 1))
	seg2 := readFile(t, segPath(src, 2))
	src.Close()

	offs := recordOffsets(ops1)
	recordOf := func(pos int) int {
		r := 0
		for i, o := range offs {
			if o <= pos {
				r = i
			}
		}
		return r
	}
	for bit := 0; bit < len(seg1)*8; bit++ {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "000001.seg"), flipBit(seg1, bit))
		writeFile(t, filepath.Join(dir, "000002.seg"), seg2)
		_, err := Open(Config{Dir: dir, MaxSegmentBytes: 1 << 20, WriteHints: false})
		if err == nil {
			t.Fatalf("bit %d: expected corruption error", bit)
		}
		if !IsKind(err, KindSegmentCorruption) {
			t.Fatalf("bit %d: kind=%v", bit, err)
		}
		e := err.(*Error)
		if e.Segment != 1 {
			t.Fatalf("bit %d: segment=%d want 1", bit, e.Segment)
		}
		if want := int64(offs[recordOf(bit/8)]); e.Offset != want {
			t.Fatalf("bit %d: offset=%d want %d", bit, e.Offset, want)
		}
	}
}

// TestSealedSegmentBitFlipWithHint 已封口段带有效提示信息时翻转一位：
// 恢复采用提示信息成功；读到被翻转记录时报段损坏，其余键正常。
func TestSealedSegmentBitFlipWithHint(t *testing.T) {
	ops1 := []op{{key: "k1", val: "v1"}, {key: "k2", val: "v2"}, {key: "k3", tombstone: true}}
	src := newTestStore(t, 1<<20)
	for _, o := range ops1 {
		if o.tombstone {
			mustDelete(t, src, o.key)
		} else {
			mustPut(t, src, o.key, o.val)
		}
	}
	rotateNow(t, src)
	mustPut(t, src, "active-key", "av")
	seg1 := readFile(t, segPath(src, 1))
	seg2 := readFile(t, segPath(src, 2))
	hint1 := readFile(t, filepath.Join(src.dir, "000001.hint"))
	src.Close()

	offs := recordOffsets(ops1)
	keyOfByte := func(pos int) string {
		r := 0
		for i, o := range offs {
			if o <= pos {
				r = i
			}
		}
		return ops1[r].key
	}
	for bit := 0; bit < len(seg1)*8; bit++ {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "000001.seg"), flipBit(seg1, bit))
		writeFile(t, filepath.Join(dir, "000001.hint"), hint1)
		writeFile(t, filepath.Join(dir, "000002.seg"), seg2)
		s, err := Open(Config{Dir: dir, MaxSegmentBytes: 1 << 20, WriteHints: true})
		if err != nil {
			t.Fatalf("bit %d: open with valid hint: %v", bit, err)
		}
		if s.Stats().HintSegments != 1 {
			t.Fatalf("bit %d: hint not adopted", bit)
		}
		badKey := keyOfByte(bit / 8)
		for _, o := range ops1 {
			_, _, err := s.Get([]byte(o.key))
			if o.key == badKey {
				if !IsKind(err, KindSegmentCorruption) {
					t.Fatalf("bit %d: get %q: kind=%v want corruption", bit, o.key, err)
				}
			} else if err != nil {
				t.Fatalf("bit %d: get %q: %v", bit, o.key, err)
			}
		}
		mustGet(t, s, "active-key", "av")
		s.Close()
	}
}

// TestTornTailVsMidCorruption 活动段最后一条记录校验失败按撕裂
// 尾部截断；中段校验失败是段损坏。
func TestTornTailVsMidCorruption(t *testing.T) {
	ops := []op{{key: "a", val: "1"}, {key: "b", val: "2"}, {key: "c", val: "3"}}
	build := func() []byte {
		s := newTestStore(t, 1<<20)
		for _, o := range ops {
			mustPut(t, s, o.key, o.val)
		}
		b := readFile(t, segPath(s, 1))
		s.Close()
		return b
	}
	full := build()
	offs := recordOffsets(ops)

	// 翻转最后一条记录 → 撕裂尾部，截断后前两条可读。
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "000001.seg"), flipBit(full, (offs[2]+2)*8))
	s, err := Open(Config{Dir: dir, MaxSegmentBytes: 1 << 20, WriteHints: true})
	if err != nil {
		t.Fatalf("torn tail: %v", err)
	}
	if got := s.Stats().TruncatedBytes; got != uint64(recordLen(ops[2])) {
		t.Fatalf("torn tail: truncated=%d want %d", got, recordLen(ops[2]))
	}
	mustGet(t, s, "a", "1")
	mustGet(t, s, "b", "2")
	mustStatus(t, s, "c", StatusNotFound)
	s.Close()

	// 翻转中间记录 → 段损坏，恢复整体失败。
	dir = t.TempDir()
	writeFile(t, filepath.Join(dir, "000001.seg"), flipBit(full, (offs[1]+2)*8))
	_, err = Open(Config{Dir: dir, MaxSegmentBytes: 1 << 20, WriteHints: true})
	if !IsKind(err, KindSegmentCorruption) {
		t.Fatalf("mid corruption: %v", err)
	}
	if e := err.(*Error); e.Offset != int64(offs[1]) {
		t.Fatalf("mid corruption: offset=%d want %d", e.Offset, offs[1])
	}
}

// TestRecoveryReplayDeterminism 相同操作序列重放得到完全相同的
// 文件与恢复结果。
func TestRecoveryReplayDeterminism(t *testing.T) {
	build := func() map[string][]byte {
		dir := t.TempDir()
		s, err := Open(Config{Dir: dir, MaxSegmentBytes: 96, WriteHints: true})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 30; i++ {
			mustPut(t, s, fmt.Sprintf("key-%d", i%7), fmt.Sprintf("val-%d", i))
			if i%5 == 0 {
				mustDelete(t, s, fmt.Sprintf("key-%d", (i+1)%7))
			}
		}
		s.Close()
		out := make(map[string][]byte)
		for _, name := range dirFiles(t, dir) {
			out[name] = readFile(t, filepath.Join(dir, name))
		}
		return out
	}
	a, b := build(), build()
	if len(a) != len(b) {
		t.Fatalf("file count differs: %d vs %d", len(a), len(b))
	}
	for name, ba := range a {
		bb, ok := b[name]
		if !ok || !bytes.Equal(ba, bb) {
			t.Fatalf("file %s differs between replays", name)
		}
	}
}
