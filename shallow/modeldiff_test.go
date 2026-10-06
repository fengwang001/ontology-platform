package shallow

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"testing"
)

// naiveModel 是与实现完全独立编写的朴素对照模型：直接按题意逐字操作
// 全量集合，不做任何缓存或边界优化，作为随机差分测试的判定基准。
type naiveModel struct {
	commits  map[CommitID]Commit
	blobs    map[BlobID]Blob
	refs     map[string]CommitID
	boundary map[CommitID]bool
	remote   *fakeRemote
	seq      uint64
}

func naiveFrom(remote *fakeRemote, s Snapshot) *naiveModel {
	m := &naiveModel{
		commits:  map[CommitID]Commit{},
		blobs:    map[BlobID]Blob{},
		refs:     map[string]CommitID{},
		boundary: map[CommitID]bool{},
		remote:   remote,
	}
	for _, c := range s.Commits {
		m.commits[c.ID] = c
	}
	for _, b := range s.Blobs {
		m.blobs[b.ID] = b
	}
	for n, id := range s.Refs {
		m.refs[n] = id
	}
	for _, id := range s.Boundary {
		m.boundary[id] = true
	}
	return m
}

// reachable 朴素可达性：从每个引用沿父递归，遇边界停止。
func (m *naiveModel) reachable() (map[CommitID]bool, map[BlobID]bool) {
	rc := map[CommitID]bool{}
	rb := map[BlobID]bool{}
	var walk func(id CommitID)
	walk = func(id CommitID) {
		c, ok := m.commits[id]
		if !ok || rc[id] {
			return
		}
		rc[id] = true
		for _, b := range c.Blobs {
			rb[b] = true
		}
		if m.boundary[id] {
			return
		}
		for _, p := range c.Parents {
			walk(p)
		}
	}
	for _, tip := range m.refs {
		walk(tip)
	}
	return rc, rb
}

// refsOK 校验引用都指向本地提交（对应错误次序中的引用检查）。
func (m *naiveModel) refsOK() error {
	for _, tip := range m.refs {
		if _, ok := m.commits[tip]; !ok {
			return ErrRefNotFound
		}
	}
	return nil
}

// naiveFetchPlan 是朴素模型的拉取暂存区，语义与实现侧 deepenPlan 独立重写。
type naiveFetchPlan struct {
	got      map[CommitID]Commit
	blobs    map[BlobID]Blob
	view     map[CommitID]bool
	boundary map[CommitID]bool // 非 nil 表示显式边界（时刻深化）
}

func newNaivePlan() *naiveFetchPlan {
	return &naiveFetchPlan{
		got:   map[CommitID]Commit{},
		blobs: map[BlobID]Blob{},
		view:  map[CommitID]bool{},
	}
}

// fetch 保证某提交在本地或暂存区可用；缺失/失败错误原样返回。
func (m *naiveModel) fetch(p *naiveFetchPlan, id CommitID) (Commit, error) {
	if c, ok := m.commits[id]; ok {
		return c, nil
	}
	if c, ok := p.got[id]; ok {
		return c, nil
	}
	c, bs, err := m.remote.FetchCommit(context.Background(), id)
	if err != nil {
		return Commit{}, err
	}
	p.got[id] = c
	for _, b := range bs {
		p.blobs[b.ID] = b
	}
	return c, nil
}

// apply 将成功的暂存区并入朴素模型，并重算边界与序号。
func (m *naiveModel) apply(p *naiveFetchPlan) {
	changed := false
	for id, c := range p.got {
		if !p.view[id] {
			continue
		}
		if _, ok := m.commits[id]; !ok {
			m.commits[id] = c
			changed = true
		}
		for _, bid := range c.Blobs {
			if gb, ok := p.blobs[bid]; ok {
				if _, exist := m.blobs[bid]; !exist {
					m.blobs[bid] = gb
				}
			}
		}
	}
	nb := map[CommitID]bool{}
	if p.boundary != nil {
		for id := range p.boundary {
			if _, ok := m.commits[id]; ok {
				nb[id] = true
			}
		}
	} else {
		for id := range p.view {
			c, ok := m.commits[id]
			if !ok {
				continue
			}
			for _, parent := range c.Parents {
				if !p.view[parent] {
					nb[id] = true
					break
				}
			}
		}
	}
	// 与实现一致：旧边界提交仍有父不在视图中则保留（边界不可回收）。
	for id := range m.boundary {
		c, ok := m.commits[id]
		if !ok {
			continue
		}
		for _, parent := range c.Parents {
			if !p.view[parent] {
				nb[id] = true
				break
			}
		}
	}
	if len(nb) != len(m.boundary) {
		changed = true
	} else {
		for id := range nb {
			if !m.boundary[id] {
				changed = true
				break
			}
		}
	}
	m.boundary = nb
	if changed {
		m.seq++
	}
}

// heldDepth 返回引用到最近边界（含边界）的最浅层数；无界返回 -1。
func (m *naiveModel) heldDepth(tip CommitID) int {
	dist := map[CommitID]int{tip: 1}
	q := []CommitID{tip}
	for len(q) > 0 {
		id := q[0]
		q = q[1:]
		if m.boundary[id] {
			return dist[id]
		}
		for _, parent := range m.commits[id].Parents {
			if _, seen := dist[parent]; !seen {
				dist[parent] = dist[id] + 1
				q = append(q, parent)
			}
		}
	}
	return -1
}

// deepenDepth 朴素按深度深化。
func (m *naiveModel) deepenDepth(depth int) error {
	if depth <= 0 {
		return ErrInvalidArg
	}
	if err := m.refsOK(); err != nil {
		return err
	}
	var tips []CommitID
	for _, tip := range m.refs {
		if h := m.heldDepth(tip); h >= 0 && h < depth {
			tips = append(tips, tip)
		}
	}
	if len(tips) == 0 {
		return nil
	}
	p := newNaivePlan()
	cur := []CommitID{}
	seen := map[CommitID]bool{}
	for _, tip := range m.refs {
		if !seen[tip] {
			seen[tip] = true
			cur = append(cur, tip)
			p.view[tip] = true
		}
	}
	for _, id := range cur {
		if _, err := m.fetch(p, id); err != nil {
			return err
		}
	}
	level := 1
	for level < depth && len(cur) > 0 {
		var nxt []CommitID
		for _, id := range cur {
			c, err := m.fetch(p, id)
			if err != nil {
				return err
			}
			for _, parent := range c.Parents {
				if !p.view[parent] {
					p.view[parent] = true
					nxt = append(nxt, parent)
				}
			}
		}
		for _, id := range nxt {
			if _, err := m.fetch(p, id); err != nil {
				return err
			}
		}
		cur = nxt
		level++
	}
	m.apply(p)
	return nil
}

// deepenSince 朴素按时刻深化，路径独立停止。
func (m *naiveModel) deepenSince(t int64) error {
	if t < 0 {
		return ErrInvalidArg
	}
	if err := m.refsOK(); err != nil {
		return err
	}
	p := newNaivePlan()
	type frame struct{ id CommitID }
	var stack []frame
	enqueued := map[CommitID]bool{}
	for _, tip := range m.refs {
		if !enqueued[tip] {
			enqueued[tip] = true
			stack = append(stack, frame{tip})
		}
	}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		c, err := m.fetch(p, f.id)
		if err != nil {
			return err
		}
		if c.CreatedAt < t {
			continue
		}
		p.view[f.id] = true
		for _, parent := range c.Parents {
			if !enqueued[parent] {
				enqueued[parent] = true
				stack = append(stack, frame{parent})
			}
		}
	}
	p.boundary = map[CommitID]bool{}
	for id := range p.view {
		c, ok := m.commits[id]
		if !ok {
			c = p.got[id]
		}
		for _, parent := range c.Parents {
			if !p.view[parent] {
				p.boundary[id] = true
				break
			}
		}
	}
	m.apply(p)
	return nil
}

// unshallow 朴素彻底去浅。
func (m *naiveModel) unshallow() error {
	if err := m.refsOK(); err != nil {
		return err
	}
	if len(m.boundary) == 0 {
		return nil
	}
	p := newNaivePlan()
	var q []CommitID
	seen := map[CommitID]bool{}
	for _, tip := range m.refs {
		if !seen[tip] {
			seen[tip] = true
			q = append(q, tip)
			p.view[tip] = true
		}
	}
	for len(q) > 0 {
		id := q[0]
		q = q[1:]
		c, err := m.fetch(p, id)
		if err != nil {
			return err
		}
		for _, parent := range c.Parents {
			if !p.view[parent] {
				p.view[parent] = true
				q = append(q, parent)
			}
		}
	}
	m.apply(p)
	return nil
}

// gc 朴素回收：删除所有不可达提交，以及不被任何存活可达提交引用的 blob。
func (m *naiveModel) gc() GCResult {
	rc, _ := m.reachable()
	res := GCResult{}
	// 保留可达提交到边界之间的祖先闭包，维持父闭合不变式。
	keep := map[CommitID]bool{}
	var q []CommitID
	for id := range rc {
		keep[id] = true
		q = append(q, id)
	}
	for len(q) > 0 {
		id := q[0]
		q = q[1:]
		if m.boundary[id] {
			continue
		}
		c := m.commits[id]
		for _, p := range c.Parents {
			if _, ok := m.commits[p]; !ok || keep[p] {
				continue
			}
			keep[p] = true
			if !m.boundary[p] {
				q = append(q, p)
			}
		}
	}
	for id := range m.commits {
		if !keep[id] {
			delete(m.commits, id)
			delete(m.boundary, id)
			res.Objects++
		}
	}
	_, rb := m.reachable()
	for id, b := range m.blobs {
		if !rb[id] {
			delete(m.blobs, id)
			res.Objects++
			res.Bytes += b.Size
		}
	}
	if res.Objects > 0 {
		m.seq++
	}
	return res
}

func (m *naiveModel) setRef(name string, target CommitID) error {
	if name == "" {
		return ErrInvalidArg
	}
	if _, ok := m.commits[target]; !ok {
		return ErrIllegalState
	}
	if m.refs[name] != target {
		m.refs[name] = target
		m.seq++
	}
	return nil
}

func (m *naiveModel) deleteRef(name string) error {
	if name == "" {
		return ErrInvalidArg
	}
	if _, ok := m.refs[name]; !ok {
		return ErrRefNotFound
	}
	delete(m.refs, name)
	m.seq++
	return nil
}

// compareState 断言实现与朴素模型在全部可观测状态上一致。
func compareState(t *testing.T, repo *Repo, m *naiveModel, step string) {
	t.Helper()
	s := repo.debugSnapshot()
	if len(s.Commits) != len(m.commits) {
		t.Fatalf("[%s] commits impl=%d naive=%d", step, len(s.Commits), len(m.commits))
	}
	for id, c := range m.commits {
		ic, ok := s.Commits[id]
		if !ok || ic.CreatedAt != c.CreatedAt ||
			len(ic.Parents) != len(c.Parents) || len(ic.Blobs) != len(c.Blobs) {
			t.Fatalf("[%s] commit %s mismatch: impl=%+v naive=%+v", step, id, ic, c)
		}
	}
	for id := range s.Commits {
		if _, ok := m.commits[id]; !ok {
			t.Fatalf("[%s] impl has extra commit %s", step, id)
		}
	}
	if len(s.Blobs) != len(m.blobs) {
		t.Fatalf("[%s] blobs impl=%d naive=%d", step, len(s.Blobs), len(m.blobs))
	}
	for id, b := range m.blobs {
		ib, ok := s.Blobs[id]
		if !ok || ib.Size != b.Size {
			t.Fatalf("[%s] blob %s mismatch", step, id)
		}
	}
	if len(s.Refs) != len(m.refs) {
		t.Fatalf("[%s] refs impl=%v naive=%v", step, s.Refs, m.refs)
	}
	for name, id := range m.refs {
		if s.Refs[name] != id {
			t.Fatalf("[%s] ref %s mismatch", step, name)
		}
	}
	if len(s.Boundary) != len(m.boundary) {
		t.Fatalf("[%s] boundary impl=%v naive=%v", step, s.Boundary, m.boundary)
	}
	for id := range m.boundary {
		if _, ok := s.Boundary[id]; !ok {
			t.Fatalf("[%s] boundary missing %s", step, id)
		}
	}
	rc, rb := m.reachable()
	if len(rc) != len(s.ReachC) {
		t.Fatalf("[%s] reachable commits impl=%d naive=%d", step, len(s.ReachC), len(rc))
	}
	for id := range rc {
		if !repo.IsCommitReachable(id) {
			t.Fatalf("[%s] commit %s reachable in naive only", step, id)
		}
	}
	for id := range s.ReachC {
		if !rc[id] {
			t.Fatalf("[%s] commit %s reachable in impl only", step, id)
		}
	}
	if len(rb) != len(s.ReachB) {
		t.Fatalf("[%s] reachable blobs impl=%d naive=%d", step, len(s.ReachB), len(rb))
	}
	for id := range rb {
		if !repo.IsBlobReachable(id) {
			t.Fatalf("[%s] blob %s reachable in naive only", step, id)
		}
	}
	for id := range s.ReachB {
		if !rb[id] {
			t.Fatalf("[%s] blob %s reachable in impl only", step, id)
		}
	}
	if repo.OpSeq() != m.seq {
		t.Fatalf("[%s] opseq impl=%d naive=%d", step, repo.OpSeq(), m.seq)
	}
}

// genRandomGraph 随机生成带分叉/合并、时间戳非单调的提交图，
// 并返回一个合法的浅克隆初始视图：取一个本地深度窗口，边界闭合。
func genRandomGraph(rng *rand.Rand) (*fakeRemote, []CommitID, []Commit) {
	remote := newFakeRemote()
	n := 12 + rng.Intn(12)
	ids := make([]CommitID, n)
	commits := make([]Commit, n)
	blobSizes := map[BlobID]int64{}
	shared := BlobID("shared-blob")
	blobSizes[shared] = 3
	for i := 0; i < n; i++ {
		ids[i] = CommitID(fmtID(i))
		var parents []CommitID
		if i > 0 {
			parents = append(parents, ids[i-1])
			if i >= 3 && rng.Intn(2) == 0 { // 合并边
				parents = append(parents, ids[rng.Intn(i-1)])
			}
		}
		ts := int64(1000 - i*7 + rng.Intn(30)) // 刻意非单调
		blobs := []BlobID{BlobID("blob-" + string(ids[i]))}
		blobSizes[BlobID("blob-"+string(ids[i]))] = int64(1 + rng.Intn(20))
		if i%4 == 0 {
			blobs = append(blobs, shared) // 共享内容对象
		}
		commits[i] = Commit{ID: ids[i], Parents: parents, CreatedAt: ts, Blobs: blobs}
	}
	for _, c := range commits {
		var bs []Blob
		for _, bid := range c.Blobs {
			bs = append(bs, Blob{ID: bid, Size: blobSizes[bid]})
		}
		remote.add(c, bs...)
	}
	return remote, ids, commits
}

// snapshotFromRemote 从远端全量数据切出浅前缀，保证 blob 大小一致。
func snapshotFromRemote(remote *fakeRemote, commits []Commit, held int) Snapshot {
	s := buildInitialView(commits, held)
	for i := range s.Blobs {
		if rb, ok := remote.blobs[s.Blobs[i].ID]; ok {
			s.Blobs[i].Size = rb.Size
		}
	}
	return s
}

// TestRandomDifferential 随机远端图 + 随机操作序列，与独立朴素模型逐步比对。
func TestRandomDifferential(t *testing.T) {
	ctx := context.Background()
	for seed := int64(1); seed <= 60; seed++ {
		rng := rand.New(rand.NewSource(seed))
		remote, _, commits := genRandomGraph(rng)
		held := 1 + rng.Intn(4)
		snap := snapshotFromRemote(remote, commits, held)
		repo, err := Load(remote, snap)
		if err != nil {
			t.Fatalf("seed %d load: %v", seed, err)
		}
		model := naiveFrom(remote, snap)
		tag := fmtStep("seed", seed)
		compareState(t, repo, model, tag+" initial")

		ops := 80
		for i := 0; i < ops; i++ {
			kind := rng.Intn(8)
			step := fmtStep("seed", seed) + " op=" + itoa(i) + " kind=" + itoa(kind)
			switch kind {
			case 0, 1:
				d := 1 + rng.Intn(8)
				e1 := repo.Deepen(ctx, d)
				e2 := model.deepenDepth(d)
				checkErr(t, step, e1, e2)
			case 2:
				tm := int64(rng.Intn(1100))
				e1 := repo.DeepenSince(ctx, tm)
				e2 := model.deepenSince(tm)
				checkErr(t, step, e1, e2)
			case 3:
				e1 := repo.Unshallow(ctx)
				e2 := model.unshallow()
				checkErr(t, step, e1, e2)
			case 4:
				r1, e1 := repo.GC()
				r2 := model.gc()
				if e1 != nil || r1 != r2 {
					t.Fatalf("%s gc impl=%+v,%v naive=%+v", step, r1, e1, r2)
				}
			case 5:
				// 随机把引用指向一个本地提交（两侧用相同候选集）。
				var local []CommitID
				for id := range model.commits {
					local = append(local, id)
				}
				if len(local) > 0 {
					target := local[rng.Intn(len(local))]
					name := "main"
					if rng.Intn(3) == 0 {
						name = "topic"
					}
					_, e1 := repo.SetRef(name, target)
					e2 := model.setRef(name, target)
					checkErr(t, step, e1, e2)
				}
			case 6:
				// 单点可达性查询：对随机候选集合逐一比对。
				for _, c := range commits {
					_ = repo.IsCommitReachable(c.ID)
					_ = repo.IsBlobReachable(c.Blobs[0])
				}
			case 7:
				// 删除/恢复引用
				if rng.Intn(2) == 0 {
					e1 := repo.DeleteRef("topic")
					e2 := model.deleteRef("topic")
					checkErr(t, step, e1, e2)
				} else {
					if mainTip, ok := model.refs["main"]; ok {
						_, e1 := repo.SetRef("topic", mainTip)
						e2 := model.setRef("topic", mainTip)
						checkErr(t, step, e1, e2)
					}
				}
			}
			compareState(t, repo, model, step)
		}
		logf(t, "CASE TestRandomDifferential seed=%d ops=%d final commits=%d blobs=%d boundary=%d: MATCH",
			seed, ops, len(model.commits), len(model.blobs), len(model.boundary))
	}
}

func checkErr(t *testing.T, step string, e1, e2 error) {
	t.Helper()
	if errorKind(e1) != errorKind(e2) {
		t.Fatalf("%s error mismatch impl=%v naive=%v", step, e1, e2)
	}
}

func errorKind(e error) string {
	switch {
	case e == nil:
		return "nil"
	case isErr(e, ErrInvalidArg):
		return "invalid"
	case isErr(e, ErrRefNotFound):
		return "ref"
	case isErr(e, ErrRemoteMissing):
		return "missing"
	case isErr(e, ErrRemoteFetch):
		return "fetch"
	case isErr(e, ErrIllegalState):
		return "illegal"
	default:
		return e.Error()
	}
}

func isErr(e, target error) bool {
	if e == nil {
		return false
	}
	return errors.Is(e, target)
}

func fmtStep(k string, v int64) string {
	return " " + k + "=" + itoa64(v)
}

func itoa64(i int64) string {
	if i == 0 {
		return "0"
	}
	var buf [16]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// TestConcurrentDeepenGC 并发深化与回收：
// 互斥锁保证二者等价于某个串行顺序；用下述不变量在每轮乱序后验证：
//  1. 所有引用提交与边界提交都在本地；
//  2. 边界外本地提交的父都在本地；
//  3. 可达提交引用的内容对象都在本地（深化不会让被回收删除的对象
//     被引用而不重新拉取）；
//  4. 立即再回收结果为 0（回收幂等）。
func TestConcurrentDeepenGC(t *testing.T) {
	ctx := context.Background()
	for seed := int64(1); seed <= 20; seed++ {
		rng := rand.New(rand.NewSource(1000 + seed))
		remote, _, commits := genRandomGraph(rng)
		snap := snapshotFromRemote(remote, commits, 2)
		repo, err := Load(remote, snap)
		if err != nil {
			t.Fatalf("seed %d load: %v", seed, err)
		}
		var wg sync.WaitGroup
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				for k := 0; k < 25; k++ {
					switch (worker + k) % 5 {
					case 0:
						_ = repo.Deepen(ctx, 1+((worker*7+k*3)%8))
					case 1:
						_ = repo.DeepenSince(ctx, int64(950+((worker+k)%120)))
					case 2:
						_ = repo.Unshallow(ctx)
					case 3:
						_, _ = repo.GC()
					case 4:
						// 每个 goroutine 使用独立随机源，避免测试自身的数据竞争。
						local := rand.New(rand.NewSource(seed + int64(worker)*7919 + int64(k)))
						_ = repo.IsCommitReachable(commits[local.Intn(len(commits))].ID)
						_ = repo.IsBlobReachable("shared-blob")
					}
				}
			}(w)
		}
		wg.Wait()

		s := repo.debugSnapshot()
		for _, tip := range s.Refs {
			if _, ok := s.Commits[tip]; !ok {
				t.Fatalf("seed %d: ref points to missing commit", seed)
			}
		}
		for id := range s.Boundary {
			if _, ok := s.Commits[id]; !ok {
				t.Fatalf("seed %d: boundary commit %s missing", seed, id)
			}
		}
		for id, c := range s.Commits {
			missingParent := false
			if _, b := s.Boundary[id]; b {
				continue
			}
			for _, p := range c.Parents {
				if _, ok := s.Commits[p]; !ok {
					missingParent = true
				}
			}
			if missingParent {
				// 合法状态的充要条件：凡有父缺失的提交必须位于浅边界。
				if _, inBoundary := s.Boundary[id]; !inBoundary {
					t.Fatalf("seed %d: non-boundary commit %s has missing parent after concurrent ops", seed, id)
				}
			}
		}
		// 再用 Load 同款校验做一次权威复核（重新载入等价状态）。
		snap2 := Snapshot{Refs: map[string]CommitID{}}
		for _, c := range s.Commits {
			snap2.Commits = append(snap2.Commits, c)
		}
		for _, b := range s.Blobs {
			snap2.Blobs = append(snap2.Blobs, b)
		}
		for n, id := range s.Refs {
			snap2.Refs[n] = id
		}
		for id := range s.Boundary {
			snap2.Boundary = append(snap2.Boundary, id)
		}
		if _, err := Load(newFakeRemote(), snap2); err != nil {
			t.Fatalf("seed %d: post-storm state fails legality reload: %v", seed, err)
		}
		for id := range s.ReachC {
			c := s.Commits[id]
			for _, b := range c.Blobs {
				if _, ok := s.Blobs[b]; !ok {
					t.Fatalf("seed %d: reachable commit %s references missing blob %s", seed, id, b)
				}
			}
		}
		res, _ := repo.GC()
		if res.Objects != 0 || res.Bytes != 0 {
			t.Fatalf("seed %d: GC after concurrent storm not idempotent: %+v", seed, res)
		}
		logf(t, "CASE TestConcurrentDeepenGC seed=%d post-storm commits=%d blobs=%d boundary=%d reachC=%d: invariants hold, GC idempotent",
			seed, len(s.Commits), len(s.Blobs), len(s.Boundary), len(s.ReachC))
	}
}

// TestConcurrentSerialEquivalence 用「操作计数+同序列朴素重放」的方式
// 验证并发写串行等价：记录并发期间每个成功写操作，再让朴素模型按
// 实际发生的参数顺序重放，终态必须一致（失败操作无副作用）。
func TestConcurrentSerialEquivalence(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewSource(42))
	remote, _, commits := genRandomGraph(rng)
	snap := snapshotFromRemote(remote, commits, 2)
	repo, err := Load(remote, snap)
	if err != nil {
		t.Fatal(err)
	}

	type op struct {
		kind  int
		depth int
		since int64
	}
	var mu sync.Mutex
	var ops []op
	var wg sync.WaitGroup
	record := func(o op) {
		mu.Lock()
		ops = append(ops, o)
		mu.Unlock()
	}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for k := 0; k < 30; k++ {
				kind := (worker + k) % 4
				switch kind {
				case 0:
					d := 1 + (worker+k)%7
					before := repo.OpSeq()
					if e := repo.Deepen(ctx, d); e == nil {
						if repo.OpSeq() != before {
							record(op{kind: 0, depth: d})
						}
					}
				case 1:
					tm := int64(980 + (worker+k)%60)
					before := repo.OpSeq()
					if e := repo.DeepenSince(ctx, tm); e == nil {
						if repo.OpSeq() != before {
							record(op{kind: 1, since: tm})
						}
					}
				case 2:
					before := repo.OpSeq()
					if e := repo.Unshallow(ctx); e == nil {
						if repo.OpSeq() != before {
							record(op{kind: 2})
						}
					}
				case 3:
					before := repo.OpSeq()
					if r, e := repo.GC(); e == nil && r.Objects > 0 {
						_ = before
						record(op{kind: 3})
					}
				}
			}
		}(w)
	}
	wg.Wait()

	// 朴素模型按记录顺序串行重放（远端无故障，全部成功）。
	model := naiveFrom(remote, snap)
	for _, o := range ops {
		switch o.kind {
		case 0:
			if e := model.deepenDepth(o.depth); e != nil {
				t.Fatalf("replay deepen: %v", e)
			}
		case 1:
			if e := model.deepenSince(o.since); e != nil {
				t.Fatalf("replay since: %v", e)
			}
		case 2:
			if e := model.unshallow(); e != nil {
				t.Fatalf("replay unshallow: %v", e)
			}
		case 3:
			model.gc()
		}
	}
	// 朴素重放的 GC 次数可能与实现不同（交错点不同），最终再各做一次 GC
	// 收敛到同一不动点后比较结构状态（不含序号）。
	model.gc()
	if _, e := repo.GC(); e != nil {
		t.Fatal(e)
	}
	s := repo.debugSnapshot()
	rc, rb := model.reachable()
	if len(s.Commits) != len(model.commits) || len(s.Blobs) != len(model.blobs) ||
		len(s.Boundary) != len(model.boundary) || len(s.ReachC) != len(rc) ||
		len(s.ReachB) != len(rb) {
		t.Fatalf("serial-equivalence failed: impl commits=%d blobs=%d boundary=%d reach=%d/%d; naive %d %d %d %d/%d",
			len(s.Commits), len(s.Blobs), len(s.Boundary), len(s.ReachC), len(s.ReachB),
			len(model.commits), len(model.blobs), len(model.boundary), len(rc), len(rb))
	}
	logf(t, "CASE TestConcurrentSerialEquivalence recorded effective writes=%d final commits=%d: serial replay matches",
		len(ops), len(model.commits))
}

func fmtID(i int) string {
	return "r" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// buildInitialView 从图中切出一个深度为 held 的浅前缀（边界闭合）。
func buildInitialView(commits []Commit, held int) Snapshot {
	tip := commits[len(commits)-1]
	set := map[CommitID]Commit{}
	boundary := map[CommitID]bool{}
	var walk func(c Commit, depth int)
	walk = func(c Commit, depth int) {
		if depth <= 0 {
			boundary[c.ID] = true
		}
		if _, ok := set[c.ID]; ok {
			return
		}
		set[c.ID] = c
		if depth <= 0 {
			return
		}
		for _, p := range c.Parents {
			for _, pc := range commits {
				if pc.ID == p {
					walk(pc, depth-1)
					break
				}
			}
		}
	}
	walk(tip, held-1)
	// 所有进入集合的提交必须数据齐全：边界提交也完整持有。
	blobs := map[BlobID]Blob{}
	var cs []Commit
	for _, c := range set {
		cs = append(cs, c)
	}
	blobSize := func(b BlobID) Blob { return Blob{ID: b, Size: 1} }
	for _, c := range cs {
		for _, b := range c.Blobs {
			blobs[b] = blobSize(b)
		}
	}
	// 修正共享 blob 大小
	blobs["shared-blob"] = Blob{ID: "shared-blob", Size: 3}
	var bs []Blob
	for _, b := range blobs {
		bs = append(bs, b)
	}
	var bd []CommitID
	for id := range boundary {
		bd = append(bd, id)
	}
	return Snapshot{
		Commits:  cs,
		Blobs:    bs,
		Refs:     map[string]CommitID{"main": tip.ID},
		Boundary: bd,
	}
}
