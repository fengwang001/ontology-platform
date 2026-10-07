package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func mustCreate(t *testing.T, s *Store, typeName, id string, initial map[string]string) {
	t.Helper()
	if _, err := s.Create(typeName, id, initial); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
}

func mustWrite(t *testing.T, s *Store, id string, baseline uint64, set map[string]string) WriteResult {
	t.Helper()
	res, err := s.Write(WriteRequest{ObjectID: id, Baseline: baseline, Set: set})
	if err != nil {
		t.Fatalf("write %s@%d %v: %v", id, baseline, set, err)
	}
	return res
}

func writeErr(t *testing.T, s *Store, id string, baseline uint64, set map[string]string) error {
	t.Helper()
	_, err := s.Write(WriteRequest{ObjectID: id, Baseline: baseline, Set: set})
	if err == nil {
		t.Fatalf("write %s@%d %v: expected rejection, got success", id, baseline, set)
	}
	return err
}

func propsOf(t *testing.T, s *Store, id string, version uint64) map[string]string {
	t.Helper()
	_, props, err := s.ReadAt(id, version)
	if err != nil {
		t.Fatalf("read %s@%d: %v", id, version, err)
	}
	return props
}

// 场景一：两次并发写入的属性子集不相交，应都成功并合并为同一基线之上的新版本。
func TestDisjointWritesBothCommitAndMerge(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{Name: "Doc"})
	mustCreate(t, s, "Doc", "d1", map[string]string{"title": "a", "body": "b"})

	r1 := mustWrite(t, s, "d1", 1, map[string]string{"title": "a2"})
	r2 := mustWrite(t, s, "d1", 1, map[string]string{"body": "b2"})

	if r1.Version != 2 || r2.Version != 3 {
		t.Fatalf("versions = %d, %d; want 2, 3", r1.Version, r2.Version)
	}
	got := propsOf(t, s, "d1", 3)
	want := map[string]string{"title": "a2", "body": "b2"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("merged state = %v, want %v", got, want)
	}
}

// 场景二：写集合直接相交，较晚进入判定的一方被拒绝。
func TestIntersectingWriteSetsConflict(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{Name: "Doc"})
	mustCreate(t, s, "Doc", "d1", map[string]string{"title": "a"})

	mustWrite(t, s, "d1", 1, map[string]string{"title": "x"})
	err := writeErr(t, s, "d1", 1, map[string]string{"title": "y"})
	if !IsKind(err, ErrKindPropertyConflict) {
		t.Fatalf("err = %v, want property conflict", err)
	}
	we := err.(*WriteError)
	if len(we.Props) != 1 || we.Props[0] != "title" {
		t.Fatalf("conflict props = %v, want [title]", we.Props)
	}
}

// 场景三：显式写集合完全不相交，但一方写入落入另一方校验钩子
// 声明的读取范围，仍判定为冲突。
func TestHookReadSetIndirectConflict(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{
		Name: "Doc",
		Hooks: []ValidationHook{{
			Name:      "needs-status",
			ReadProps: []string{"status"},
		}},
	})
	mustCreate(t, s, "Doc", "d1", map[string]string{"title": "a", "status": "draft"})

	// 第一次写入只写 title，但其足迹包含钩子读集合 {status}。
	mustWrite(t, s, "d1", 1, map[string]string{"title": "b"})
	// 第二次写入只写 status：写集合不相交，但与读集合相交。
	err := writeErr(t, s, "d1", 1, map[string]string{"status": "published"})
	if !IsKind(err, ErrKindPropertyConflict) {
		t.Fatalf("err = %v, want property conflict via hook read set", err)
	}
}

// 场景三对照：写集合与相关读集合均不相交时，两次写入都成功。
func TestHookReadSetDisjointBothCommit(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{
		Name: "Doc",
		Hooks: []ValidationHook{{
			Name: "needs-status",
			// 仅当写入触及 title 时才声明读取 status。
			DeclareReads: func(writeSet map[string]struct{}) ([]string, []RemoteRead) {
				if _, ok := writeSet["title"]; ok {
					return []string{"status"}, nil
				}
				return nil, nil
			},
		}},
	})
	mustCreate(t, s, "Doc", "d1", map[string]string{"title": "a", "status": "draft", "hits": "0"})

	mustWrite(t, s, "d1", 1, map[string]string{"title": "b"})
	// hits 既不在对方写集合，也不在本次写入声明的读集合中，允许合并。
	r := mustWrite(t, s, "d1", 1, map[string]string{"hits": "1"})
	if r.Version != 3 {
		t.Fatalf("version = %d, want 3", r.Version)
	}
}

// 场景三（动态声明版）：一方写入落入另一方钩子按写入声明的读取范围。
func TestHookDynamicReadSetIndirectConflict(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{
		Name: "Doc",
		Hooks: []ValidationHook{{
			Name: "title-needs-status",
			DeclareReads: func(writeSet map[string]struct{}) ([]string, []RemoteRead) {
				if _, ok := writeSet["title"]; ok {
					return []string{"status"}, nil
				}
				return nil, nil
			},
		}},
	})
	mustCreate(t, s, "Doc", "d1", map[string]string{"title": "a", "status": "draft"})

	// 第一次写入 title，声明读取 status，足迹为 {title, status}。
	mustWrite(t, s, "d1", 1, map[string]string{"title": "b"})
	// 第二次写入 status：显式写集合不相交，但落入对方读集合。
	err := writeErr(t, s, "d1", 1, map[string]string{"status": "published"})
	if !IsKind(err, ErrKindPropertyConflict) {
		t.Fatalf("err = %v, want property conflict via dynamic read set", err)
	}
}

// 已删除检查优先于属性级冲突检查；三类错误互斥且可通过返回值区分。
func TestDeletedTakesPriorityOverPropertyConflict(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{Name: "Doc"})
	mustCreate(t, s, "Doc", "d1", map[string]string{"title": "a"})
	mustWrite(t, s, "d1", 1, map[string]string{"title": "b"})
	if err := s.Delete("d1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// 该写入既满足属性级冲突（title 相交），也落在已删除实例上：
	// 裁定必须为 deleted，证明检查顺序。
	err := writeErr(t, s, "d1", 1, map[string]string{"title": "c"})
	if !IsKind(err, ErrKindDeleted) {
		t.Fatalf("err = %v, want deleted", err)
	}
	if IsKind(err, ErrKindPropertyConflict) || IsKind(err, ErrKindStaleBaseline) {
		t.Fatalf("error kinds are not mutually exclusive: %v", err)
	}
}

// 基线落后超过一个版本时返回版本冲突，且与属性级冲突可区分。
func TestStaleBaselineConflict(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{Name: "Doc"})
	mustCreate(t, s, "Doc", "d1", map[string]string{"a": "1"})
	mustWrite(t, s, "d1", 1, map[string]string{"b": "2"})
	mustWrite(t, s, "d1", 2, map[string]string{"c": "3"})

	err := writeErr(t, s, "d1", 1, map[string]string{"d": "4"})
	if !IsKind(err, ErrKindStaleBaseline) {
		t.Fatalf("err = %v, want stale baseline", err)
	}
	// 基线等于当前版本时不存在落后，直接成功。
	mustWrite(t, s, "d1", 3, map[string]string{"d": "4"})
}

// 被拒绝的写入不得改变任何外部可观察状态：
// 版本号、快照字节、当前属性均保持不变。
func TestRejectedWriteChangesNothing(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{Name: "Doc"})
	mustCreate(t, s, "Doc", "d1", map[string]string{"title": "a"})
	mustWrite(t, s, "d1", 1, map[string]string{"title": "b"})

	beforeBytes, beforeProps, err := s.ReadAt("d1", 2)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	beforeVersion, _ := s.Version("d1")
	beforeDecisions := len(s.Decisions())

	writeErr(t, s, "d1", 1, map[string]string{"title": "c"})

	afterVersion, _ := s.Version("d1")
	if afterVersion != beforeVersion {
		t.Fatalf("rejected write advanced version: %d -> %d", beforeVersion, afterVersion)
	}
	afterBytes, afterProps, err := s.ReadAt("d1", 2)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatalf("rejected write changed snapshot bytes")
	}
	if fmt.Sprint(beforeProps) != fmt.Sprint(afterProps) {
		t.Fatalf("rejected write changed props: %v -> %v", beforeProps, afterProps)
	}
	// 判定日志新增一条拒绝记录：拒绝路径与成功路径在外部可区分。
	decisions := s.Decisions()
	if len(decisions) != beforeDecisions+1 {
		t.Fatalf("decision log grew by %d, want 1", len(decisions)-beforeDecisions)
	}
	last := decisions[len(decisions)-1]
	if last.Verdict != VerdictPropertyConflict || last.NewVersion != 0 {
		t.Fatalf("rejection record = %+v", last)
	}
}

// 快照字节级不变：读取某版本后无论再发生多少次写入，
// 针对该版本的读取结果保持不变。
func TestSnapshotByteImmutability(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{Name: "Doc"})
	mustCreate(t, s, "Doc", "d1", map[string]string{"a": "1"})

	first, _, err := s.ReadAt("d1", 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for i := 0; i < 20; i++ {
		v, _ := s.Version("d1")
		mustWrite(t, s, "d1", v, map[string]string{fmt.Sprintf("k%d", i): "v"})
	}
	second, _, err := s.ReadAt("d1", 1)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("snapshot bytes changed: %q vs %q", first, second)
	}
}

// 版本号严格单调递增且不重复。
func TestVersionsStrictlyMonotonic(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{Name: "Doc"})
	mustCreate(t, s, "Doc", "d1", map[string]string{"a": "1"})

	seen := map[uint64]bool{}
	prev, _ := s.Version("d1")
	for i := 0; i < 50; i++ {
		r := mustWrite(t, s, "d1", prev, map[string]string{fmt.Sprintf("k%d", i): "v"})
		if r.Version <= prev {
			t.Fatalf("version not increasing: %d -> %d", prev, r.Version)
		}
		if seen[r.Version] {
			t.Fatalf("duplicate version %d", r.Version)
		}
		seen[r.Version] = true
		prev = r.Version
	}
}

// 校验钩子拒绝合并后的预期状态，属于与三类冲突正交的第四类结果。
func TestValidationHookRejects(t *testing.T) {
	s := NewStore()
	s.RegisterObjectType(ObjectType{
		Name: "Doc",
		Hooks: []ValidationHook{{
			Name: "status-enum",
			Validate: func(values map[string]string) error {
				if values["status"] == "bogus" {
					return errors.New("invalid status")
				}
				return nil
			},
		}},
	})
	mustCreate(t, s, "Doc", "d1", map[string]string{"status": "draft"})

	err := writeErr(t, s, "d1", 1, map[string]string{"status": "bogus"})
	if !IsKind(err, ErrKindValidation) {
		t.Fatalf("err = %v, want validation", err)
	}
	v, _ := s.Version("d1")
	if v != 1 {
		t.Fatalf("validation rejection advanced version to %d", v)
	}
}
