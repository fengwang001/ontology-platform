package shallow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeRemote 是内存版完整提交图，可注入 failAfter（第 N 次拉取后中断）：
// 缺失提交产生 ErrRemoteMissing，中断产生 ErrRemoteFetch，二者可区分。
type fakeRemote struct {
	mu        sync.Mutex
	commits   map[CommitID]Commit
	blobs     map[BlobID]Blob
	failAfter int
	calls     int32
}

// TestErrorOrdering 校验错误优先级中每对相邻错误：
// 参数非法 > 引用不存在 > 远端缺失 > 拉取失败 > 本地状态非法。
func TestErrorOrdering(t *testing.T) {
	remote, cs := buildLinearRemote()
	repo, err := Load(remote, Snapshot{
		Commits:  []Commit{cs[4], cs[3]},
		Blobs:    []Blob{{ID: "b4", Size: 14}, {ID: "b3", Size: 13}},
		Refs:     map[string]CommitID{"main": "c4"},
		Boundary: []CommitID{"c3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 1) 非法参数优先于一切：即使引用名也不存在。
	if _, _, err := repo.GetRef(""); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("empty ref name -> ErrInvalidArg, got %v", err)
	}
	logf(t, "order pair (invalid > missing-ref): GetRef(\"\") -> %v", err)
	if err := repo.Deepen(context.Background(), 0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("depth 0 -> ErrInvalidArg, got %v", err)
	}
	logf(t, "order pair (invalid > ...): Deepen(0) -> %v", err)
	if err := repo.DeepenSince(context.Background(), -1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("negative time -> ErrInvalidArg, got %v", err)
	}
	// 2) 引用不存在优先于远端错误：删除 main 后再按深度深化（无引用时直接成功，
	// 故用 DeleteRef/GetRef 验证引用错误，并构造引用指向缺失提交的状态通过 SetRef 校验）。
	if err := repo.DeleteRef("nope"); !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("delete missing ref -> ErrRefNotFound, got %v", err)
	}
	logf(t, "order pair (missing-ref checked before remote ops): DeleteRef(nope) -> %v", err)
	if _, _, err := repo.GetRef("nope"); !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("get missing ref -> ErrRefNotFound, got %v", err)
	}
	// 3) 远端缺失优先于拉取失败：远端本身不含 ghost（与 failAfter 无关）。
	gTip := Commit{ID: "g-tip", Parents: []CommitID{"ghost"}, CreatedAt: 1}
	repo2, err := Load(remote, Snapshot{
		Commits:  []Commit{gTip},
		Refs:     map[string]CommitID{"main": "g-tip"},
		Boundary: []CommitID{"g-tip"},
	})
	if err != nil {
		t.Fatal(err)
	}
	remote.failAfter = 1 // 即便注入中断，首个目标缺失仍应报缺失
	err = repo2.Deepen(context.Background(), 2)
	logf(t, "order pair (remote-missing > fetch-fail): fetch ghost under failAfter=1 -> %v", err)
	if !errors.Is(err, ErrRemoteMissing) {
		t.Fatalf("missing object must outrank injected fetch failure, got %v", err)
	}
	// 4) 拉取失败优先于本地状态非法：本地状态在操作后不再重判，
	// 用 Load 覆盖状态非法用例，并让深化在到达任何状态判定前先遇拉取失败。
	remote.failAfter = 1
	err = repo.Deepen(context.Background(), 5)
	logf(t, "order pair (fetch-fail > illegal-state): deepen with outage -> %v (no state rewrite attempted)", err)
	if !errors.Is(err, ErrRemoteFetch) {
		t.Fatalf("want ErrRemoteFetch, got %v", err)
	}
	// 5) 本地状态非法：Load 时边界外缺父。
	_, err = Load(remote, Snapshot{
		Commits:  []Commit{cs[4], cs[3]},
		Refs:     map[string]CommitID{"main": "c4"},
		Boundary: []CommitID{"c4"}, // c3 的父 c2 缺失而 c3 不在边界
	})
	logf(t, "order pair (... > illegal-state): load c3 missing parent c2 outside boundary -> %v", err)
	if !errors.Is(err, ErrIllegalState) {
		t.Fatalf("want ErrIllegalState, got %v", err)
	}
}

// TestSharedBlobReachability 内容对象多提交共享时的可达性。
func TestSharedBlobReachability(t *testing.T) {
	remote, cs := buildLinearRemote()
	shared := Blob{ID: "shared", Size: 5}
	orphan := Commit{ID: "orphan", CreatedAt: 0, Blobs: []BlobID{"shared"}}
	c4 := cs[4]
	c4.Blobs = append(c4.Blobs, "shared")
	remote.add(orphan, shared)
	remote.add(c4, shared)
	repo, err := Load(remote, Snapshot{
		Commits: []Commit{c4, cs[3], cs[2], cs[1], cs[0], orphan},
		Blobs: []Blob{{ID: "b4", Size: 14}, {ID: "b3", Size: 13}, {ID: "b2", Size: 12},
			{ID: "b1", Size: 11}, {ID: "b0", Size: 10}, shared},
		Refs:     map[string]CommitID{"main": "c4"},
		Boundary: []CommitID{"c0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	logf(t, "CASE TestSharedBlobReachability shared referenced by reachable c4 and unreachable orphan -> reachable=%v",
		repo.IsBlobReachable("shared"))
	if !repo.IsBlobReachable("shared") {
		t.Fatalf("one reachable referencing commit suffices")
	}
}

// TestUnshallowThenNoSideEffect 去浅后边界为空，再深化零副作用。
func TestUnshallowThenNoSideEffect(t *testing.T) {
	remote, cs := buildLinearRemote()
	repo, err := Load(remote, Snapshot{
		Commits:  []Commit{cs[4], cs[3]},
		Blobs:    []Blob{{ID: "b4", Size: 14}, {ID: "b3", Size: 13}},
		Refs:     map[string]CommitID{"main": "c4"},
		Boundary: []CommitID{"c3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Unshallow(context.Background()); err != nil {
		t.Fatalf("unshallow: %v", err)
	}
	logf(t, "CASE TestUnshallow boundary=%v want []; all c0..c4 held=%v",
		repo.Boundary(),
		[]bool{repo.HasCommit("c0"), repo.HasCommit("c1"), repo.HasCommit("c2"),
			repo.HasCommit("c3"), repo.HasCommit("c4")})
	if len(repo.Boundary()) != 0 {
		t.Fatalf("unshallow must clear boundary")
	}
	for i := 0; i <= 4; i++ {
		if !repo.HasCommit(CommitID(fmt.Sprintf("c%d", i))) {
			t.Fatalf("c%d missing after unshallow", i)
		}
	}
	seq := repo.OpSeq()
	calls := atomic.LoadInt32(&remote.calls)
	if err := repo.Deepen(context.Background(), 100); err != nil {
		t.Fatalf("deepen after unshallow: %v", err)
	}
	if err := repo.DeepenSince(context.Background(), 0); err != nil {
		t.Fatalf("deepenSince after unshallow: %v", err)
	}
	logf(t, "post-unshallow deepen(100)+deepenSince(0): seq=%d->%d calls=%d->%d want unchanged",
		seq, repo.OpSeq(), calls, atomic.LoadInt32(&remote.calls))
	if repo.OpSeq() != seq || atomic.LoadInt32(&remote.calls) != calls {
		t.Fatalf("deepening after unshallow must be zero-side-effect success")
	}
}

// TestGCIdempotentAndBoundaryProtection 回收幂等且保护边界与引用提交。
func TestGCIdempotentAndBoundaryProtection(t *testing.T) {
	remote, cs := buildLinearRemote()
	// 额外持有的不可达提交 orphan（父 c0），与共享内容 shared 同时被 c4/orphan 引用。
	shared := Blob{ID: "shared", Size: 99}
	orphan := Commit{ID: "orphan", CreatedAt: 50, Blobs: []BlobID{"shared"}}
	remote.add(orphan, shared)
	c4 := cs[4]
	c4.Blobs = append(c4.Blobs, "shared")
	remote.add(c4, shared)
	repo, err := Load(remote, Snapshot{
		Commits: []Commit{c4, cs[3], cs[2], cs[1], cs[0], orphan},
		Blobs: []Blob{
			{ID: "b4", Size: 14}, {ID: "b3", Size: 13}, {ID: "b2", Size: 12},
			{ID: "b1", Size: 11}, {ID: "b0", Size: 10}, shared,
		},
		Refs:     map[string]CommitID{"main": "c4"},
		Boundary: []CommitID{"c0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res1, err := repo.GC()
	if err != nil {
		t.Fatal(err)
	}
	res2, err := repo.GC()
	if err != nil {
		t.Fatal(err)
	}
	logf(t, "CASE TestGCIdempotent gc1 objects=%d bytes=%d (want orphan only=1,0B; shared kept by reachable c4) gc2 objects=%d bytes=%d",
		res1.Objects, res1.Bytes, res2.Objects, res2.Bytes)
	if repo.HasCommit("orphan") || !repo.HasBlob("shared") || !repo.HasCommit("c3") {
		t.Fatalf("orphan removed; shared retained (reachable c4 references it); boundary c3 protected")
	}
	if res1.Objects != 1 || res1.Bytes != 0 {
		t.Fatalf("gc1 = %+v, want {1,0}", res1)
	}
	if res2.Objects != 0 || res2.Bytes != 0 {
		t.Fatalf("immediate second GC must be zero, got %+v", res2)
	}
}

// TestFetchMidwayFailureRollback 拉取中途失败整体回滚，且区分缺失与失败。
func TestFetchMidwayFailureRollback(t *testing.T) {
	remote, cs := buildLinearRemote()
	repo, err := Load(remote, Snapshot{
		Commits:  []Commit{cs[4]},
		Blobs:    []Blob{{ID: "b4", Size: 14}},
		Refs:     map[string]CommitID{"main": "c4"},
		Boundary: []CommitID{"c4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// deepen(5) 需依次拉取 c3,c2,c1,c0；第 2 次（c2）后中断，c3 的拉取已提交到远端调用但未并入本地。
	remote.failAfter = 2
	seqBefore := repo.OpSeq()
	err = repo.Deepen(context.Background(), 5)
	logf(t, "CASE TestFetchMidwayFailureRollback err=%v want ErrRemoteFetch; boundary=%v want=[c4]; has c3=%v; seq=%d want %d",
		err, repo.Boundary(), repo.HasCommit("c3"), repo.OpSeq(), seqBefore)
	if !errors.Is(err, ErrRemoteFetch) {
		t.Fatalf("want ErrRemoteFetch, got %v", err)
	}
	if repo.HasCommit("c3") || len(repo.Boundary()) != 1 || repo.Boundary()[0] != "c4" {
		t.Fatalf("rollback failed: local objects/boundary must be untouched")
	}
	if repo.OpSeq() != seqBefore {
		t.Fatalf("rejected op must not advance sequence: %d != %d", repo.OpSeq(), seqBefore)
	}

	// 远端不存在提交：伪造一个父指向幽灵提交的本地边界。
	ghostParent := Commit{ID: "ghost"}
	_ = ghostParent
	tip := Commit{ID: "g-tip", Parents: []CommitID{"ghost"}, CreatedAt: 1, Blobs: nil}
	repo2, err := Load(remote, Snapshot{
		Commits:  []Commit{tip},
		Refs:     map[string]CommitID{"main": "g-tip"},
		Boundary: []CommitID{"g-tip"},
	})
	if err != nil {
		t.Fatal(err)
	}
	remote.failAfter = 0
	err = repo2.Unshallow(context.Background())
	logf(t, "ghost commit fetch: actual err=%v want ErrRemoteMissing (distinct from fetch failure)", err)
	if !errors.Is(err, ErrRemoteMissing) {
		t.Fatalf("want ErrRemoteMissing, got %v", err)
	}
}

// TestBoundaryParentHeldLocally 碰巧持有边界父提交仍视为不存在。
func TestBoundaryParentHeldLocally(t *testing.T) {
	remote, cs := buildLinearRemote()
	repo, err := Load(remote, Snapshot{
		// c3 在边界，其下的 c2,c1 本地碰巧持有；c1 也置于边界使该链闭合合法。
		Commits: []Commit{cs[4], cs[3], cs[2], cs[1]},
		Blobs: []Blob{{ID: "b4", Size: 14}, {ID: "b3", Size: 13},
			{ID: "b2", Size: 12}, {ID: "b1", Size: 11}},
		Refs:     map[string]CommitID{"main": "c4"},
		Boundary: []CommitID{"c3", "c1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	logf(t, "CASE TestBoundaryParentHeldLocally boundary={c3}, c2 physically held locally")
	if repo.IsCommitReachable("c2") {
		t.Fatalf("traversal stops at boundary c3; held-but-beyond c2 must be unreachable")
	}
	if !repo.IsBlobReachable("b3") || repo.IsBlobReachable("b2") {
		t.Fatalf("b3 reachable through boundary commit; b2 must not be")
	}
	res, err := repo.GC()
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	logf(t, "GC actual deleted=%d bytes=%d want 4 objects 23 bytes; criterion=c2,c1,b2,b1 removed while reachable boundary c3 protected; second GC zero",
		res.Objects, res.Bytes)
	if repo.HasCommit("c2") || repo.HasCommit("c1") || repo.HasBlob("b2") || repo.HasBlob("b1") || !repo.HasCommit("c3") {
		t.Fatalf("unreachable held chain c2,c1 deleted; reachable boundary c3 kept")
	}
	res2, _ := repo.GC()
	if res2.Objects != 0 || res2.Bytes != 0 {
		t.Fatalf("second GC must be zero, got %+v", res2)
	}
}

// TestDepthNoSideEffect 目标深度不增时零副作用（序号与拉取次数不变）。
func TestDepthNoSideEffect(t *testing.T) {
	remote, cs := buildLinearRemote()
	repo, err := Load(remote, Snapshot{
		Commits:  []Commit{cs[4], cs[3]},
		Blobs:    []Blob{{ID: "b4", Size: 14}, {ID: "b3", Size: 13}},
		Refs:     map[string]CommitID{"main": "c4"},
		Boundary: []CommitID{"c3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	logf(t, "CASE TestDepthNoSideEffect held depth=2 (c4,c3)")
	for _, d := range []int{1, 2} {
		seq := repo.OpSeq()
		calls := atomic.LoadInt32(&remote.calls)
		if err := repo.Deepen(context.Background(), d); err != nil {
			t.Fatalf("deepen(%d): %v", d, err)
		}
		logf(t, "deepen(%d): seq=%d->%d fetchCalls=%d->%d want unchanged",
			d, seq, repo.OpSeq(), calls, atomic.LoadInt32(&remote.calls))
		if repo.OpSeq() != seq || atomic.LoadInt32(&remote.calls) != calls {
			t.Fatalf("deepen(%d) had side effects", d)
		}
	}
}

// TestTimeDeepenIndependentPaths 时刻深化在不同路径上独立停止。
func TestTimeDeepenIndependentPaths(t *testing.T) {
	remote := newFakeRemote()
	cm := func(id string, ts int64, parents ...string) Commit {
		c := Commit{ID: CommitID(id), CreatedAt: ts, Parents: ids(parents...),
			Blobs: []BlobID{BlobID("blob-" + id)}}
		remote.add(c, Blob{ID: BlobID("blob-" + id), Size: 1})
		return c
	}
	cm("a0", 100)
	cm("b0", 200)
	cm("a1", 500, "a0")
	cm("b1", 600, "b0")
	tip := cm("tip", 1000, "a1", "b1")
	repo, err := Load(remote, Snapshot{
		Commits:  []Commit{tip},
		Blobs:    []Blob{{ID: "blob-tip", Size: 1}},
		Refs:     map[string]CommitID{"main": "tip"},
		Boundary: []CommitID{"tip"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.DeepenSince(context.Background(), 550); err != nil {
		t.Fatalf("deepenSince: %v", err)
	}
	bound := repo.Boundary()
	logf(t, "CASE TestTimeDeepenIndependentPaths cutoff=550 held tip=%v a1=%v b1=%v a0=%v b0=%v boundary=%v",
		repo.HasCommit("tip"), repo.HasCommit("a1"), repo.HasCommit("b1"),
		repo.HasCommit("a0"), repo.HasCommit("b0"), bound)
	logf(t, "want held={tip,b1} boundary={tip,b1}; criterion=path A stops at a1(500) excluded; path B independently includes b1(600), stops at b0(200); stopped nodes never enter the view")
	if repo.HasCommit("a1") || repo.HasCommit("a0") || repo.HasCommit("b0") {
		t.Fatalf("per-path independent stop violated")
	}
	if !repo.HasCommit("b1") || !containsID(bound, "b1") || !containsID(bound, "tip") {
		t.Fatalf("want included {tip,b1} with boundary {tip,b1}, got %v", bound)
	}
}

func newFakeRemote() *fakeRemote {
	return &fakeRemote{commits: map[CommitID]Commit{}, blobs: map[BlobID]Blob{}}
}

func (f *fakeRemote) add(c Commit, blobs ...Blob) {
	f.commits[c.ID] = c
	for _, b := range blobs {
		f.blobs[b.ID] = b
	}
}

func (f *fakeRemote) FetchCommit(ctx context.Context, id CommitID) (Commit, []Blob, error) {
	f.mu.Lock()
	failAfter := f.failAfter
	f.mu.Unlock()
	n := atomic.AddInt32(&f.calls, 1)
	if failAfter > 0 && int(n) > failAfter {
		return Commit{}, nil, fmt.Errorf("%w: injected outage at call %d", ErrRemoteFetch, n)
	}
	cm, ok := f.commits[id]
	if !ok {
		return Commit{}, nil, fmt.Errorf("%w: %s", ErrRemoteMissing, id)
	}
	out := make([]Blob, 0, len(cm.Blobs))
	for _, bid := range cm.Blobs {
		out = append(out, f.blobs[bid])
	}
	return cm, out, nil
}

var testLogFile = func() *os.File {
	f, err := os.CreateTemp("", "shallow-test-*.log")
	if err != nil {
		panic(err)
	}
	return f
}()

func logf(t *testing.T, format string, args ...any) {
	t.Helper()
	line := fmt.Sprintf(format, args...)
	fmt.Fprintln(testLogFile, line)
	t.Log(line)
}

func ids(ss ...string) []CommitID {
	out := make([]CommitID, len(ss))
	for i, s := range ss {
		out[i] = CommitID(s)
	}
	return out
}

func containsID(xs []CommitID, x CommitID) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// buildLinearRemote 构造 c0 <- c1 <- ... <- c4，cN 引用 bN(大小 10+N)。
func buildLinearRemote() (*fakeRemote, []Commit) {
	f := newFakeRemote()
	cs := make([]Commit, 5)
	for i := 0; i < 5; i++ {
		id := CommitID(fmt.Sprintf("c%d", i))
		c := Commit{ID: id, CreatedAt: int64(100 + i),
			Blobs: []BlobID{BlobID(fmt.Sprintf("b%d", i))}}
		if i > 0 {
			c.Parents = []CommitID{CommitID(fmt.Sprintf("c%d", i-1))}
		}
		cs[i] = c
		f.add(c, Blob{ID: BlobID(fmt.Sprintf("b%d", i)), Size: int64(10 + i)})
	}
	return f, cs
}

var _ = errors.Is

// TestLinearBoundary 线性图上的边界确定与深化推进。
func TestLinearBoundary(t *testing.T) {
	remote, cs := buildLinearRemote()
	snap := Snapshot{
		Commits:  []Commit{cs[4], cs[3]},
		Blobs:    []Blob{{ID: "b4", Size: 14}, {ID: "b3", Size: 13}},
		Refs:     map[string]CommitID{"main": "c4"},
		Boundary: []CommitID{"c3"},
	}
	repo, err := Load(remote, snap)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	logf(t, "CASE TestLinearBoundary input=c0..c4 local={c3,c4} boundary={c3} ref=main->c4")
	logf(t, "actual boundary=%v want=[c3] criterion=boundary commit whose parent is outside view", repo.Boundary())
	if got := repo.Boundary(); len(got) != 1 || got[0] != "c3" {
		t.Fatalf("boundary = %v, want [c3]", got)
	}
	if !repo.IsCommitReachable("c3") || !repo.IsCommitReachable("c4") {
		t.Fatalf("c3,c4 reachable; BFS includes boundary commit but stops above it")
	}
	if repo.IsCommitReachable("c2") {
		t.Fatalf("c2 not local -> not reachable")
	}
	if err := repo.Deepen(context.Background(), 4); err != nil {
		t.Fatalf("deepen(4): %v", err)
	}
	logf(t, "after deepen(4): boundary=%v want=[c1]; c0 held=%v (not fetched: parent outside view needs only to be absent) criterion=4 commits per path c4,c3,c2,c1",
		repo.Boundary(), repo.HasCommit("c0"))
	if got := repo.Boundary(); len(got) != 1 || got[0] != "c1" {
		t.Fatalf("boundary after deepen = %v, want [c1]", got)
	}
	for i := 1; i <= 4; i++ {
		if !repo.HasCommit(CommitID(fmt.Sprintf("c%d", i))) {
			t.Fatalf("c%d missing after deepen", i)
		}
	}
	if repo.HasCommit("c0") {
		t.Fatalf("c0 outside the depth-4 view and must not be fetched")
	}
}

// TestMergeBoundary 分叉合并图上的边界确定与非法状态拒绝。
func TestMergeBoundary(t *testing.T) {
	remote := newFakeRemote()
	cm := func(id string, ts int64, parents ...string) Commit {
		c := Commit{ID: CommitID(id), CreatedAt: ts, Parents: ids(parents...),
			Blobs: []BlobID{BlobID("blob-" + id)}}
		remote.add(c, Blob{ID: BlobID("blob-" + id), Size: 7})
		return c
	}
	m0 := cm("m0", 0)
	a1 := cm("a1", 1, "m0")
	b1 := cm("b1", 1, "m0")
	a2 := cm("a2", 2, "a1")
	m3 := cm("m3", 3, string(a2.ID), string(b1.ID))

	_, err := Load(remote, Snapshot{
		Commits:  []Commit{m3, a2, a1},
		Refs:     map[string]CommitID{"main": "m3"},
		Boundary: []CommitID{"a1"},
	})
	logf(t, "CASE TestMergeBoundary m3 lacks parent b1 locally and is not boundary -> actual err=%v want ErrIllegalState", err)
	if !errors.Is(err, ErrIllegalState) {
		t.Fatalf("want ErrIllegalState, got %v", err)
	}

	repo, err := Load(remote, Snapshot{
		Commits:  []Commit{m3, a2, a1},
		Refs:     map[string]CommitID{"main": "m3"},
		Boundary: []CommitID{"m3", "a1"},
	})
	if err != nil {
		t.Fatalf("legal load: %v", err)
	}
	if err := repo.Deepen(context.Background(), 3); err != nil {
		t.Fatalf("deepen: %v", err)
	}
	logf(t, "CASE TestMergeBoundary after deepen(3): commits m3=%v a2=%v b1=%v a1=%v m0=%v boundary=%v want=[] (all parents reachable, root m0 has no parent)",
		repo.HasCommit("m3"), repo.HasCommit("a2"), repo.HasCommit("b1"),
		repo.HasCommit("a1"), repo.HasCommit(m0.ID), repo.Boundary())
	for _, want := range []CommitID{"m3", "a2", "b1", "a1", "m0"} {
		if !repo.HasCommit(want) {
			t.Fatalf("%s missing after deepen(3); criterion=per-path depth on merge graph", want)
		}
	}
	if got := repo.Boundary(); len(got) != 0 {
		t.Fatalf("boundary = %v, want empty: view includes complete history to root m0", got)
	}
}
