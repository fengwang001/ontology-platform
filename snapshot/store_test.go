package snapshot

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func init() {
	// 判定逻辑由 TestDecisionLogging 单独校验；其余用例静默以保持输出可读。
	SetLogOutput(io.Discard)
}

// refSnap 是朴素参照模型中的快照。
type refSnap struct {
	id    int64
	t     time.Time
	files map[string]struct{}
}

// naiveRef 用最直白的 map 切片方式重放全部规则，用于交叉校验。
type naiveRef struct {
	snaps    []refSnap
	everUsed map[string]struct{}
	nextID   int64
}

func newNaiveRef() *naiveRef {
	return &naiveRef{everUsed: map[string]struct{}{}, nextID: 1}
}

type refResult struct {
	id            int64
	retainedIDs   []int64
	expiredIDs    []int64
	deletedFiles  []string
	snapFileLists map[int64][]string
	storage       []string
}

// commit 按规范逐条重放提交+过期+删除。
func (r *naiveRef) commit(t time.Time, added, removed []string, retain int, threshold time.Time) (*refResult, error) {
	if retain < 0 {
		return nil, ErrInvalidArgument
	}
	if dupOrEmpty(added) || dupOrEmpty(removed) {
		return nil, ErrInvalidArgument
	}
	if intersects(added, removed) {
		return nil, ErrInvalidArgument
	}
	var current map[string]struct{}
	if len(r.snaps) > 0 {
		current = r.snaps[len(r.snaps)-1].files
		if !t.After(r.snaps[len(r.snaps)-1].t) {
			return nil, ErrTimeNotIncreasing
		}
	} else {
		current = map[string]struct{}{}
	}
	for _, name := range removed {
		if _, ok := current[name]; !ok {
			return nil, ErrFileNotInCurrent
		}
	}
	for _, name := range added {
		if _, ok := r.everUsed[name]; ok {
			return nil, ErrFileNameReused
		}
	}

	files := map[string]struct{}{}
	for name := range current {
		files[name] = struct{}{}
	}
	for _, name := range removed {
		delete(files, name)
	}
	for _, name := range added {
		files[name] = struct{}{}
		r.everUsed[name] = struct{}{}
	}
	newID := r.nextID
	r.snaps = append(r.snaps, refSnap{id: newID, t: t, files: files})
	r.nextID++

	// 三条件取并。
	var kept []refSnap
	var expired []refSnap
	for i, sp := range r.snaps {
		isCurrent := i == len(r.snaps)-1
		recent := retain > 0 && i >= len(r.snaps)-retain
		fresh := sp.t.After(threshold)
		if isCurrent || recent || fresh {
			kept = append(kept, sp)
		} else {
			expired = append(expired, sp)
		}
	}

	retainedRefs := map[string]int{}
	for _, sp := range kept {
		for name := range sp.files {
			retainedRefs[name]++
		}
	}
	deletedSet := map[string]struct{}{}
	for _, sp := range expired {
		for name := range sp.files {
			if retainedRefs[name] == 0 {
				deletedSet[name] = struct{}{}
			}
		}
	}

	var deleted []string
	for name := range deletedSet {
		deleted = append(deleted, name)
	}
	slices.Sort(deleted)

	r.snaps = kept

	res := &refResult{id: newID, snapFileLists: map[int64][]string{}}
	for _, sp := range kept {
		res.retainedIDs = append(res.retainedIDs, sp.id)
		var fl []string
		for name := range sp.files {
			fl = append(fl, name)
		}
		slices.Sort(fl)
		res.snapFileLists[sp.id] = fl
	}
	for _, sp := range expired {
		res.expiredIDs = append(res.expiredIDs, sp.id)
	}
	res.deletedFiles = deleted
	storage := map[string]struct{}{}
	for _, sp := range kept {
		for name := range sp.files {
			storage[name] = struct{}{}
		}
	}
	for name := range storage {
		res.storage = append(res.storage, name)
	}
	slices.Sort(res.storage)
	return res, nil
}

func dupOrEmpty(names []string) bool {
	seen := map[string]struct{}{}
	for _, name := range names {
		if name == "" {
			return true
		}
		if _, ok := seen[name]; ok {
			return true
		}
		seen[name] = struct{}{}
	}
	return false
}

func intersects(a, b []string) bool {
	set := map[string]struct{}{}
	for _, name := range a {
		set[name] = struct{}{}
	}
	for _, name := range b {
		if _, ok := set[name]; ok {
			return true
		}
	}
	return false
}

func snapshotSignature(s *Store) string {
	var b strings.Builder
	for _, sp := range s.ListSnapshots() {
		b.WriteString("snap:")
		b.WriteString(sp.CommitTime.Format(time.RFC3339Nano))
		b.WriteString(":")
		b.WriteString(strings.Join(sp.Files, ","))
		b.WriteString("|")
	}
	b.WriteString("files:")
	b.WriteString(strings.Join(s.ListFiles(), ","))
	return b.String()
}

func assertAgainstRef(t *testing.T, s *Store, res *CommitResult, ref *refResult) {
	t.Helper()
	gotIDs := make([]int64, len(res.Retained))
	for i, sp := range res.Retained {
		gotIDs[i] = sp.ID
	}
	live := s.ListSnapshots()
	if err := compareWithRef(s, res, ref); err != nil {
		t.Fatal(err)
	}
	for _, sp := range live {
		want := ref.snapFileLists[sp.ID]
		got, err := s.GetSnapshot(sp.ID)
		if err != nil {
			t.Fatalf("GetSnapshot(%d): %v", sp.ID, err)
		}
		if !slices.Equal(got.Files, want) {
			t.Fatalf("GetSnapshot(%d) files: got %v want %v", sp.ID, got.Files, want)
		}
	}
	if s.CurrentSnapshotID() != ref.retainedIDs[len(ref.retainedIDs)-1] {
		t.Fatalf("current snapshot id mismatch: got %d want %d",
			s.CurrentSnapshotID(), ref.retainedIDs[len(ref.retainedIDs)-1])
	}
}

func commitAndCompare(t *testing.T, s *Store, ref *naiveRef, at time.Time, added, removed []string, retain int, threshold time.Time) *CommitResult {
	t.Helper()
	res, err := s.Commit(at, added, removed, retain, threshold)
	if err != nil {
		t.Fatalf("Commit(%v,%v,%v) unexpected error: %v", added, removed, at, err)
	}
	rr, rerr := ref.commit(at, added, removed, retain, threshold)
	if rerr != nil {
		t.Fatalf("naive ref rejected valid commit: %v", rerr)
	}
	assertAgainstRef(t, s, res, rr)
	return res
}

// TestRetainUnionAndDeletion 覆盖三条件取并、当前快照永不过期、
// 引用计数删除，并与朴素参照逐步对照。
func TestRetainUnionAndDeletion(t *testing.T) {
	s := NewStore()
	ref := newNaiveRef()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// s1: {a,b}
	commitAndCompare(t, s, ref, base.Add(1*time.Hour), []string{"a", "b"}, nil, 1, base)
	// s2: {b,c}，retain=2，两者都因“最近”保留
	commitAndCompare(t, s, ref, base.Add(2*time.Hour), []string{"c"}, []string{"a"}, 2, base)
	// s3: {c,d}，阈值=base+90min：s1 既不新鲜也不在最近1个 -> 过期；
	// s2 时间戳>阈值 -> 保留；a 仅被 s1 引用 -> 删除；b 仍被 s2 引用 -> 存活。
	res := commitAndCompare(t, s, ref, base.Add(3*time.Hour), []string{"d"}, []string{"b"}, 1, base.Add(90*time.Minute))
	if !slices.Equal(res.ExpiredIDs, []int64{1}) {
		t.Fatalf("s1 should expire, got expired=%v", res.ExpiredIDs)
	}
	if !slices.Equal(res.DeletedFiles, []string{"a"}) {
		t.Fatalf("only a should be deleted, got %v", res.DeletedFiles)
	}
	// 当前快照永不过期：即使阈值在未来且 retain=0，s4 仍保留；
	// s2={b,c}、s3={c,d} 过期；b,c 删除；d 被当前 s4={d,e} 引用而存活。
	res = commitAndCompare(t, s, ref, base.Add(4*time.Hour), []string{"e"}, []string{"c"}, 0, base.Add(100000*time.Hour))
	if !slices.Equal(res.ExpiredIDs, []int64{2, 3}) {
		t.Fatalf("s2,s3 should expire, got %v", res.ExpiredIDs)
	}
	if !slices.Equal(res.DeletedFiles, []string{"b", "c"}) {
		t.Fatalf("b,c deleted, got %v", res.DeletedFiles)
	}
	if !s.FileExists("d") || !s.FileExists("e") {
		t.Fatalf("d,e must survive, storage=%v", s.ListFiles())
	}
	if s.CurrentSnapshotID() != 4 {
		t.Fatalf("current snapshot must be 4, got %d", s.CurrentSnapshotID())
	}

	// s5/s6：retain=3 且阈值=base+3h，最近规则与时间戳规则的 AND 分支也会被打印。
	commitAndCompare(t, s, ref, base.Add(5*time.Hour), []string{"f"}, nil, 3, base.Add(3*time.Hour))
	commitAndCompare(t, s, ref, base.Add(6*time.Hour), []string{"g"}, nil, 3, base.Add(3*time.Hour))

	if _, err := s.GetSnapshot(1); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("expired snapshot lookup should fail, got %v", err)
	}
	if _, err := s.GetSnapshot(6); err != nil {
		t.Fatalf("current snapshot must be readable: %v", err)
	}
}

// TestInvalidInputsRejected 覆盖每一种可区分错误，且拒绝后状态不变。
func TestInvalidInputsRejected(t *testing.T) {
	s := NewStore()
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	if _, err := s.Commit(base.Add(time.Hour), []string{"a", "b"}, nil, 100, base); err != nil {
		t.Fatal(err)
	}
	// 让 b 变成“已物理删除”的文件名，验证复用仍被拒绝。
	if _, err := s.Commit(base.Add(2*time.Hour), []string{"c"}, []string{"b"}, 0, base.Add(100000*time.Hour)); err != nil {
		t.Fatal(err)
	}

	cur := base.Add(3 * time.Hour)
	cases := []struct {
		name    string
		added   []string
		removed []string
		at      time.Time
		retain  int
		want    error
	}{
		{"negative retain", []string{"x"}, nil, cur, -1, ErrInvalidArgument},
		{"empty added name", []string{""}, nil, cur, 1, ErrInvalidArgument},
		{"dup added", []string{"x", "x"}, nil, cur, 1, ErrInvalidArgument},
		{"dup removed", []string{"x"}, []string{"a", "a"}, cur, 1, ErrInvalidArgument},
		{"add remove overlap", []string{"a"}, []string{"a"}, cur, 1, ErrInvalidArgument},
		{"time in past", []string{"x"}, nil, base, 1, ErrTimeNotIncreasing},
		{"time equal", []string{"x"}, nil, base.Add(2 * time.Hour), 1, ErrTimeNotIncreasing},
		{"removed missing", nil, []string{"zzz"}, cur, 1, ErrFileNotInCurrent},
		{"name reused live", []string{"a"}, nil, cur, 1, ErrFileNameReused},
		{"name reused after deletion", []string{"b"}, nil, cur, 1, ErrFileNameReused},
	}

	errKinds := map[string]int{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := snapshotSignature(s)
			res, err := s.Commit(c.at, c.added, c.removed, c.retain, base)
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v (res=%v)", c.want, err, res)
			}
			if res != nil {
				t.Fatalf("rejected commit must return nil result")
			}
			after := snapshotSignature(s)
			if after != before {
				t.Fatalf("state changed after rejection:\nbefore=%s\nafter=%s", before, after)
			}
			errKinds[c.want.Error()]++
			if err := s.CheckInvariants(); err != nil {
				t.Fatalf("invariants broken after rejection: %v", err)
			}
		})
	}

	// 错误类别必须互不相同、可区分。
	all := []error{ErrInvalidArgument, ErrTimeNotIncreasing, ErrFileNameReused, ErrFileNotInCurrent, ErrSnapshotNotFound}
	seen := map[string]struct{}{}
	for _, e := range all {
		if _, dup := seen[e.Error()]; dup {
			t.Fatalf("duplicate error sentinel message: %v", e)
		}
		seen[e.Error()] = struct{}{}
	}
	if len(errKinds) != 4 {
		t.Fatalf("rejections must cover 4 distinct error kinds, got %d", len(errKinds))
	}
}

// TestRandomizedAgainstNaiveRef 随机操作流，每一步都与朴素参照全量比对。
func TestRandomizedAgainstNaiveRef(t *testing.T) {
	s := NewStore()
	ref := newNaiveRef()
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	var fileCounter int
	nextFile := func() string {
		fileCounter++
		return "f" + itoa(fileCounter)
	}

	t0 := base
	for step := 0; step < 300; step++ {
		t0 = t0.Add(time.Duration(1+(step%7)) * time.Minute)

		live := s.ListSnapshots()
		var currentFiles []string
		if len(live) > 0 {
			currentFiles = live[len(live)-1].Files
		}

		var added, removed []string
		nAdded := step % 4
		for i := 0; i < nAdded; i++ {
			// 偶尔复用已有名字，触发拒绝路径。
			if step%11 == 0 && len(currentFiles) > 0 {
				added = append(added, currentFiles[step%len(currentFiles)])
			} else {
				added = append(added, nextFile())
			}
		}
		if len(currentFiles) > 0 && step%3 == 0 {
			removed = append(removed, currentFiles[step%len(currentFiles)])
		}
		if step%17 == 0 {
			removed = append(removed, "missing-file")
		}

		retain := []int{0, 1, 2, 3, 5}[step%5]
		threshold := base.Add(time.Duration(step%200) * time.Minute)

		res, err := s.Commit(t0, added, removed, retain, threshold)
		rr, rerr := ref.commit(t0, added, removed, retain, threshold)
		if (err == nil) != (rerr == nil) {
			t.Fatalf("step %d error mismatch: store=%v ref=%v (added=%v removed=%v retain=%d at=%s threshold=%s)",
				step, err, rerr, added, removed, retain, t0.Format(time.RFC3339), threshold.Format(time.RFC3339))
		}
		if err != nil {
			if !errors.Is(err, rerr) {
				t.Fatalf("step %d error kind mismatch: store=%v ref=%v (added=%v removed=%v retain=%d)",
					step, err, rerr, added, removed, retain)
			}
			continue
		}
		if ierr := compareWithRef(s, res, rr); ierr != nil {
			t.Fatalf("step %d: %v (added=%v removed=%v retain=%d at=%s threshold=%s)",
				step, ierr, added, removed, retain, t0.Format(time.RFC3339), threshold.Format(time.RFC3339))
		}
	}
}

func compareWithRef(s *Store, res *CommitResult, ref *refResult) error {
	if res.NewSnapshotID != ref.id {
		return fmt.Errorf("new snapshot id: got %d want %d", res.NewSnapshotID, ref.id)
	}
	if !slices.Equal(res.ExpiredIDs, ref.expiredIDs) {
		return fmt.Errorf("expired ids: got %v want %v", res.ExpiredIDs, ref.expiredIDs)
	}
	if !slices.Equal(res.DeletedFiles, ref.deletedFiles) {
		return fmt.Errorf("deleted files: got %v want %v", res.DeletedFiles, ref.deletedFiles)
	}
	gotIDs := make([]int64, len(res.Retained))
	for i, sp := range res.Retained {
		gotIDs[i] = sp.ID
	}
	if !slices.Equal(gotIDs, ref.retainedIDs) {
		return fmt.Errorf("retained ids: got %v want %v", gotIDs, ref.retainedIDs)
	}
	live := s.ListSnapshots()
	if len(live) != len(ref.retainedIDs) {
		return fmt.Errorf("live snapshot count: got %d want %d", len(live), len(ref.retainedIDs))
	}
	for _, sp := range live {
		want := ref.snapFileLists[sp.ID]
		if !slices.Equal(sp.Files, want) {
			return fmt.Errorf("snapshot %d files: got %v want %v", sp.ID, sp.Files, want)
		}
	}
	if !slices.Equal(s.ListFiles(), ref.storage) {
		return fmt.Errorf("storage files: got %v want %v", s.ListFiles(), ref.storage)
	}
	return s.CheckInvariants()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestConcurrentReadsAndCommits 多执行体并发查询/自检与提交过期，
// 以 -race 运行验证无数据竞争且始终读到自洽状态。
func TestConcurrentReadsAndCommits(t *testing.T) {
	s := NewStore()
	base := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	const commits = 200
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= commits; i++ {
			added := []string{"p" + itoa(i)}
			var removed []string
			if i >= 2 {
				removed = []string{"p" + itoa(i-1)}
			}
			if _, err := s.Commit(base.Add(time.Duration(i)*time.Minute), added, removed, 5, base.Add(100000*time.Hour)); err != nil {
				t.Errorf("commit %d: %v", i, err)
				return
			}
		}
	}()

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < commits; i++ {
				s.ListSnapshots()
				s.ListFiles()
				s.CurrentSnapshotID()
				id := int64(1 + (i % (commits + 1)))
				if _, err := s.GetSnapshot(id); err != nil && !errors.Is(err, ErrSnapshotNotFound) {
					t.Errorf("GetSnapshot(%d): %v", id, err)
					return
				}
				s.FileExists("p" + itoa(i+1))
				if err := s.CheckInvariants(); err != nil {
					t.Errorf("invariants: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if err := s.CheckInvariants(); err != nil {
		t.Fatalf("final invariants: %v", err)
	}
	live := s.ListSnapshots()
	if len(live) != 5 {
		t.Fatalf("retain=5 => 5 live snapshots, got %d", len(live))
	}
	files := s.ListFiles()
	wantFiles := []string{"p196", "p197", "p198", "p199", "p200"}
	if !slices.Equal(files, wantFiles) {
		t.Fatalf("storage=%v want %v", files, wantFiles)
	}
}

// TestDecisionLogging 校验日志包含每步输入、保留快照、删除文件及判定依据。
func TestDecisionLogging(t *testing.T) {
	var buf bytes.Buffer
	prev := logger
	SetLogOutput(&buf)
	defer func() {
		loggerMu.Lock()
		logger = prev
		loggerMu.Unlock()
	}()

	s := NewStore()
	base := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Commit(base.Add(1*time.Hour), []string{"a", "b"}, nil, 1, base); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(base.Add(2*time.Hour), nil, nil, 1, base.Add(90*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(base.Add(3*time.Hour), []string{"c"}, []string{"a"}, 0, base.Add(100000*time.Hour)); err != nil {
		t.Fatal(err)
	}

	log := buf.String()
	for _, want := range []string{
		"Commit begin", "retainCount=0",
		"snapshot 1 expired", "snapshot 2 expired",
		"snapshot 3 retained: reason=current snapshot",
		`file "a" deleted`,
		"Commit done",
		"deletedFiles=[a",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q\n--- log ---\n%s", want, log)
		}
	}
}
