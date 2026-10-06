package logkv

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// buildSegments 构建每段一条记录的存储，返回各段写入的键。
func buildSegments(t *testing.T, segOps [][]op) *Store {
	t.Helper()
	s := newTestStore(t, 1<<20)
	for i, ops := range segOps {
		for _, o := range ops {
			if o.tombstone {
				mustDelete(t, s, o.key)
			} else {
				mustPut(t, s, o.key, o.val)
			}
		}
		if i < len(segOps)-1 {
			rotateNow(t, s)
		}
	}
	return s
}

// snapshotAll 读取全部键的当前结果。
func snapshotAll(t *testing.T, s *Store, keys []string) map[string]op {
	t.Helper()
	out := make(map[string]op)
	for _, k := range keys {
		val, st, err := s.Get([]byte(k))
		if err != nil {
			t.Fatalf("snapshot get %q: %v", k, err)
		}
		switch st {
		case StatusFound:
			out[k] = op{key: k, val: string(val)}
		case StatusDeleted:
			out[k] = op{key: k, tombstone: true}
		}
	}
	return out
}

func checkSnapshot(t *testing.T, s *Store, want map[string]op, keys []string) {
	t.Helper()
	for _, k := range keys {
		w, ok := want[k]
		switch {
		case !ok:
			mustStatus(t, s, k, StatusNotFound)
		case w.tombstone:
			mustStatus(t, s, k, StatusDeleted)
		default:
			mustGet(t, s, k, w.val)
		}
	}
}

func allKeys(segOps [][]op) []string {
	seen := map[string]bool{}
	var keys []string
	for _, ops := range segOps {
		for _, o := range ops {
			if !seen[o.key] {
				seen[o.key] = true
				keys = append(keys, o.key)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

func scanSegFile(t *testing.T, path string) []scannedRecord {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	st, _ := f.Stat()
	res, err := scanSegment(0, f, st.Size(), false)
	if err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return res.records
}

func TestMergeBasic(t *testing.T) {
	segOps := [][]op{
		{{key: "a", val: "1"}, {key: "b", val: "1"}},
		{{key: "a", val: "2"}, {key: "c", val: "1"}},
		{{key: "d", val: "1"}},
		{{key: "e", val: "1"}},
	}
	s := buildSegments(t, segOps)
	defer s.Close()
	keys := allKeys(segOps)
	before := snapshotAll(t, s, keys)
	if err := s.Merge([]uint32{1, 2}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	checkSnapshot(t, s, before, keys)
	// 文件布局：输出段复用最小段号 1，段 2 被删除。
	if !fileExists(segPath(s, 1)) || fileExists(segPath(s, 2)) {
		t.Fatalf("files: %v", dirFiles(t, s.dir))
	}
	if !fileExists(segPath(s, 3)) || !fileExists(segPath(s, 4)) {
		t.Fatalf("files: %v", dirFiles(t, s.dir))
	}
	// 输出段只含每键最新记录：a=2, b=1, c=1。
	recs := scanSegFile(t, segPath(s, 1))
	if len(recs) != 3 {
		t.Fatalf("output records=%d want 3", len(recs))
	}
	s2 := reopen(t, s)
	defer s2.Close()
	checkSnapshot(t, s2, before, keys)
}

// TestMergeTombstoneDropped 删除标记在合并集合外无更小写序号的
// 记录时被丢弃。
func TestMergeTombstoneDropped(t *testing.T) {
	segOps := [][]op{
		{{key: "x", val: "old"}},
		{{key: "x", tombstone: true}},
		{{key: "y", val: "keep"}},
	}
	s := buildSegments(t, segOps)
	defer s.Close()
	mustStatus(t, s, "x", StatusDeleted)
	if err := s.Merge([]uint32{1, 2}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	// 输出段不含 x 的任何记录。
	for _, r := range scanSegFile(t, segPath(s, 1)) {
		if string(r.Key) == "x" {
			t.Fatalf("tombstone for x should be dropped")
		}
	}
	// 删除标记已被合并清除，与从未出现不可区分。
	mustStatus(t, s, "x", StatusNotFound)
	mustGet(t, s, "y", "keep")
	s2 := reopen(t, s)
	defer s2.Close()
	mustStatus(t, s2, "x", StatusNotFound)
}

// TestMergeTombstoneKept 合并集合外的段里存在该键写序号更小的
// 记录时，删除标记必须保留，防止旧值复活。
func TestMergeTombstoneKept(t *testing.T) {
	segOps := [][]op{
		{{key: "x", val: "old"}},      // 段 1：旧值，不参与合并
		{{key: "x", tombstone: true}}, // 段 2：删除标记，参与合并
		{{key: "z", val: "1"}},        // 段 3
	}
	s := buildSegments(t, segOps)
	defer s.Close()
	if err := s.Merge([]uint32{2}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	// 输出段（段号 2）保留删除标记。
	found := false
	for _, r := range scanSegFile(t, segPath(s, 2)) {
		if string(r.Key) == "x" {
			found = true
			if !r.Tombstone {
				t.Fatalf("kept record for x must be tombstone")
			}
		}
	}
	if !found {
		t.Fatalf("tombstone for x must be kept (old value exists in segment 1)")
	}
	mustStatus(t, s, "x", StatusDeleted)
	// 进一步合并段 1 与段 2 后，旧值与删除标记一起消失。
	if err := s.Merge([]uint32{1, 2}); err != nil {
		t.Fatalf("merge2: %v", err)
	}
	mustStatus(t, s, "x", StatusNotFound)
	for _, r := range scanSegFile(t, segPath(s, 1)) {
		if string(r.Key) == "x" {
			t.Fatalf("x should be gone after merging all its segments")
		}
	}
}

// TestMergeNonContiguous 被合并集合不连续。
func TestMergeNonContiguous(t *testing.T) {
	var segOps [][]op
	for i := 0; i < 6; i++ {
		segOps = append(segOps, []op{{key: fmt.Sprintf("k%d", i), val: fmt.Sprintf("v%d", i)}})
	}
	s := buildSegments(t, segOps)
	defer s.Close()
	keys := allKeys(segOps)
	before := snapshotAll(t, s, keys)
	if err := s.Merge([]uint32{1, 3, 5}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	checkSnapshot(t, s, before, keys)
	for _, id := range []uint32{3, 5} {
		if fileExists(segPath(s, id)) {
			t.Fatalf("segment %d should be deleted", id)
		}
	}
	for _, id := range []uint32{1, 2, 4, 6} {
		if !fileExists(segPath(s, id)) {
			t.Fatalf("segment %d should exist: %v", id, dirFiles(t, s.dir))
		}
	}
	s2 := reopen(t, s)
	defer s2.Close()
	checkSnapshot(t, s2, before, keys)
}

// TestMergeCrashStages 在合并的每个崩溃点崩溃后重放恢复。
func TestMergeCrashStages(t *testing.T) {
	stages := []string{"tmp-written", "meta-written", "sealed", "hint-written", "files-deleted"}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			segOps := [][]op{
				{{key: "a", val: "1"}, {key: "b", val: "1"}},
				{{key: "a", val: "2"}, {key: "b", tombstone: true}},
				{{key: "c", val: "1"}},
			}
			build := func(dir string) *Store {
				s, err := Open(Config{Dir: dir, MaxSegmentBytes: 1 << 20, WriteHints: true})
				if err != nil {
					t.Fatal(err)
				}
				for i, ops := range segOps {
					for _, o := range ops {
						if o.tombstone {
							mustDelete(t, s, o.key)
						} else {
							mustPut(t, s, o.key, o.val)
						}
					}
					if i < len(segOps)-1 {
						rotateNow(t, s)
					}
				}
				return s
			}
			keys := allKeys(segOps)
			// 参照库：完成一次干净合并，得到合并后的权威快照
			// （被清除的删除标记变为从未出现）。
			ref := build(t.TempDir())
			if err := ref.Merge([]uint32{1, 2}); err != nil {
				t.Fatalf("ref merge: %v", err)
			}
			afterMerge := snapshotAll(t, ref, keys)
			ref.Close()

			dir := t.TempDir()
			s := build(dir)
			before := snapshotAll(t, s, keys)
			s.testHook = func(got string) {
				if got == stage {
					panic("crash")
				}
			}
			func() {
				defer func() { recover() }()
				s.Merge([]uint32{1, 2})
			}()
			// 崩溃后重开：恢复必须清理到一致状态。
			s2, err := Open(Config{Dir: dir, MaxSegmentBytes: 1 << 20, WriteHints: true})
			if err != nil {
				t.Fatalf("reopen after crash at %s: %v", stage, err)
			}
			defer s2.Close()
			// 封口前崩溃：合并未生效；封口后崩溃：合并已生效。
			want := before
			if stage == "sealed" || stage == "hint-written" || stage == "files-deleted" {
				want = afterMerge
			}
			checkSnapshot(t, s2, want, keys)
			// 不残留临时文件与元信息。
			for _, name := range dirFiles(t, dir) {
				if filepath.Ext(name) == ".tmp" || filepath.Ext(name) == ".mmeta" {
					t.Fatalf("leftover %s after crash at %s", name, stage)
				}
			}
			// 再次重开结果一致。
			s2.Close()
			s3, err := Open(Config{Dir: dir, MaxSegmentBytes: 1 << 20, WriteHints: true})
			if err != nil {
				t.Fatalf("reopen2: %v", err)
			}
			defer s3.Close()
			checkSnapshot(t, s3, want, keys)
		})
	}
}

// TestMergeErrorKinds 错误类别可区分且只报次序最靠前的一类。
func TestMergeErrorKinds(t *testing.T) {
	segOps := [][]op{
		{{key: "a", val: "1"}},
		{{key: "b", val: "1"}},
	}
	s := buildSegments(t, segOps)
	defer s.Close()
	// 参数非法。
	if err := s.Merge(nil); !IsKind(err, KindInvalidArgument) {
		t.Fatalf("empty: %v", err)
	}
	if err := s.Merge([]uint32{1, 1}); !IsKind(err, KindInvalidArgument) {
		t.Fatalf("dup: %v", err)
	}
	// 段不存在。
	if err := s.Merge([]uint32{1, 99}); !IsKind(err, KindSegmentNotFound) {
		t.Fatalf("notfound: %v", err)
	}
	// 活动段不可合并。
	if err := s.Merge([]uint32{2}); !IsKind(err, KindActiveSegmentNotMergeable) {
		t.Fatalf("active: %v", err)
	}
	// 优先级：参数非法 > 段不存在 > 活动段不可合并。
	if err := s.Merge([]uint32{99, 99}); !IsKind(err, KindInvalidArgument) {
		t.Fatalf("dup+notfound: %v", err)
	}
	if err := s.Merge([]uint32{2, 99}); !IsKind(err, KindSegmentNotFound) {
		t.Fatalf("active+notfound: %v", err)
	}
}

// TestMergeConcurrentReadWrite 合并不影响并发读取与写入的结果。
func TestMergeConcurrentReadWrite(t *testing.T) {
	var segOps [][]op
	for i := 0; i < 4; i++ {
		segOps = append(segOps, []op{
			{key: fmt.Sprintf("k%d", i), val: fmt.Sprintf("v%d", i)},
			{key: "shared", val: fmt.Sprintf("s%d", i)},
		})
	}
	s := buildSegments(t, segOps)
	defer s.Close()
	keys := allKeys(segOps)
	before := snapshotAll(t, s, keys)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			key := fmt.Sprintf("k%d", i%4)
			if _, _, err := s.Get([]byte(key)); err != nil {
				t.Errorf("get: %v", err)
				return
			}
			if err := s.Put([]byte("live"), []byte(fmt.Sprintf("%d", i))); err != nil {
				t.Errorf("put: %v", err)
				return
			}
			i++
		}
	}()
	if err := s.Merge([]uint32{1, 2, 3}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	close(stop)
	wg.Wait()
	checkSnapshot(t, s, before, keys)
	s2 := reopen(t, s)
	defer s2.Close()
	checkSnapshot(t, s2, before, keys)
}

// TestMergePreservesSeq 合并输出保留原写序号，恢复后写序号继续
// 从全局最大值加一分配。
func TestMergePreservesSeq(t *testing.T) {
	segOps := [][]op{
		{{key: "a", val: "1"}},
		{{key: "a", val: "2"}},
		{{key: "b", val: "1"}},
	}
	s := buildSegments(t, segOps)
	defer s.Close()
	next := s.NextSeq()
	if err := s.Merge([]uint32{1, 2}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	recs := scanSegFile(t, segPath(s, 1))
	if len(recs) != 1 || recs[0].Seq != 2 {
		t.Fatalf("output should contain only seq=2, got %+v", recs)
	}
	s2 := reopen(t, s)
	defer s2.Close()
	if got := s2.NextSeq(); got != next {
		t.Fatalf("next seq after merge+reopen=%d want %d", got, next)
	}
}

// TestMergeMetaCorrupt 已封口输出段的元信息校验失败时恢复报错。
func TestMergeMetaCorrupt(t *testing.T) {
	segOps := [][]op{
		{{key: "a", val: "1"}},
		{{key: "a", val: "2"}},
		{{key: "b", val: "1"}},
	}
	s := buildSegments(t, segOps)
	if err := s.Merge([]uint32{1, 2}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	dir := s.dir
	s.Close()
	// 伪造一份损坏的元信息并重新出现被替代的段，模拟崩溃现场。
	writeFile(t, filepath.Join(dir, "000001.mmeta"), []byte("LKM1-garbage"))
	_, err := Open(Config{Dir: dir, MaxSegmentBytes: 1 << 20, WriteHints: true})
	if !IsKind(err, KindSegmentCorruption) {
		t.Fatalf("corrupt merge meta: %v", err)
	}
}
