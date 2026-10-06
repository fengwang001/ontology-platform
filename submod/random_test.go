package submod

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// naive 是独立的朴素对照模型：用线性扫描与直接递归实现同一套语义，
// 不复用协调器的任何状态机逻辑，用于随机操作序列的差分比对。
type naive struct {
	store *Store
	super RepoID
	table []SubmoduleRecord
	ws    map[string]Worktree
	gen   uint64
}

type naiveNode struct {
	path string
	rec  SubmoduleRecord
	repo *Repo
	pin  *Commit
}

func (n *naive) expandAll() ([]naiveNode, error) {
	var out []naiveNode
	var rec func(tbl Table, prefix string, chain map[RepoID]bool) error
	rec = func(tbl Table, prefix string, chain map[RepoID]bool) error {
		for _, r := range tbl {
			full := joinPath(prefix, r.Path)
			if chain[r.Repo] {
				return fmt.Errorf("%w: %q", ErrCycle, r.Repo)
			}
			repo := n.store.Get(r.Repo)
			var pin *Commit
			if repo != nil {
				pin = repo.Commits[r.Pinned]
			}
			out = append(out, naiveNode{path: full, rec: r, repo: repo, pin: pin})
			if pin != nil {
				chain[r.Repo] = true
				if err := rec(pin.Table, full, chain); err != nil {
					return err
				}
				delete(chain, r.Repo)
			}
		}
		return nil
	}
	root := Table{}
	for _, r := range n.table {
		root[r.Path] = r
	}
	err := rec(root, "", map[RepoID]bool{n.super: true})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (n *naive) align() error {
	nodes, err := n.expandAll()
	if err != nil {
		return err
	}
	for _, nd := range nodes {
		if nd.repo == nil || nd.pin == nil {
			return fmt.Errorf("%w: %q", ErrDanglingPin, nd.path)
		}
	}
	for _, nd := range nodes {
		if wt, ok := n.ws[nd.path]; ok && wt.Dirty {
			return fmt.Errorf("%w: %q", ErrDirty, nd.path)
		}
	}
	for _, nd := range nodes {
		n.ws[nd.path] = Worktree{Checkout: nd.rec.Pinned}
	}
	n.gen++
	return nil
}

func (n *naive) advance() error {
	var tracked []SubmoduleRecord
	for _, r := range n.table {
		if r.Tracking != "" {
			tracked = append(tracked, r)
		}
	}
	if len(tracked) == 0 {
		return nil
	}
	for _, r := range tracked {
		if n.store.Get(r.Repo) == nil {
			return fmt.Errorf("%w: %q", ErrRepoNotFound, r.Repo)
		}
	}
	tips := map[string]CommitID{}
	for _, r := range tracked {
		tip, ok := n.store.Get(r.Repo).Tip(r.Tracking)
		if !ok {
			return fmt.Errorf("%w: %q", ErrBranchNotFound, r.Tracking)
		}
		tips[r.Path] = tip
	}
	for _, r := range tracked {
		if !n.store.Get(r.Repo).HasCommit(r.Pinned) {
			return fmt.Errorf("%w: %q", ErrDanglingPin, r.Path)
		}
	}
	for _, r := range tracked {
		if wt, ok := n.ws[r.Path]; ok && wt.Dirty {
			return fmt.Errorf("%w: %q", ErrDirty, r.Path)
		}
	}
	for _, r := range tracked {
		if !n.store.Get(r.Repo).IsDescendantOrEqual(tips[r.Path], r.Pinned) {
			return fmt.Errorf("%w: %q", ErrNonFastForward, r.Path)
		}
	}
	for i, r := range n.table {
		if tip, ok := tips[r.Path]; ok {
			r.Pinned = tip
			n.table[i] = r
		}
	}
	n.gen++
	return nil
}

func (n *naive) add(path string, repo RepoID, pin CommitID, tracking string) error {
	if repo == "" {
		return fmt.Errorf("%w: empty repo", ErrInvalidParam)
	}
	np, err := NormalizePath(path)
	if err != nil {
		return err
	}
	if repo == n.super {
		return fmt.Errorf("%w: self mount", ErrCycle)
	}
	for _, r := range n.table {
		if pathsConflict(r.Path, np) {
			return fmt.Errorf("%w: %q vs %q", ErrPathConflict, np, r.Path)
		}
	}
	if nodes, terr := n.expandAll(); terr == nil {
		for _, nd := range nodes {
			if pathsConflict(nd.path, np) {
				return fmt.Errorf("%w: %q vs nested %q", ErrPathConflict, np, nd.path)
			}
		}
	}
	rp := n.store.Get(repo)
	if rp == nil {
		return fmt.Errorf("%w: %q", ErrRepoNotFound, repo)
	}
	if tracking != "" {
		if _, ok := rp.Tip(tracking); !ok {
			return fmt.Errorf("%w: %q", ErrBranchNotFound, tracking)
		}
	}
	if !rp.HasCommit(pin) {
		return fmt.Errorf("%w: %q", ErrDanglingPin, pin)
	}
	chain := map[RepoID]bool{n.super: true, repo: true}
	var walk func(c *Commit) error
	walk = func(cm *Commit) error {
		for _, r := range cm.Table {
			if chain[r.Repo] {
				return fmt.Errorf("%w: %q", ErrCycle, r.Repo)
			}
			rr := n.store.Get(r.Repo)
			if rr == nil {
				continue
			}
			sub, ok := rr.Commits[r.Pinned]
			if !ok {
				continue
			}
			chain[r.Repo] = true
			if err := walk(sub); err != nil {
				return err
			}
			delete(chain, r.Repo)
		}
		return nil
	}
	if err := walk(rp.Commits[pin]); err != nil {
		return err
	}
	n.table = append(n.table, SubmoduleRecord{Path: np, Repo: repo, Pinned: pin, Tracking: tracking})
	n.gen++
	return nil
}

func (n *naive) remove(path string, force bool) error {
	np, err := NormalizePath(path)
	if err != nil {
		return err
	}
	idx := -1
	for i, r := range n.table {
		if r.Path == np {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("%w: no mount %q", ErrInvalidParam, np)
	}
	if !force {
		for p, wt := range n.ws {
			if wt.Dirty && (p == np || strings.HasPrefix(p, np+"/")) {
				return fmt.Errorf("%w: %q", ErrDirty, p)
			}
		}
	}
	n.table = append(n.table[:idx], n.table[idx+1:]...)
	for p := range n.ws {
		if p == np || strings.HasPrefix(p, np+"/") {
			delete(n.ws, p)
		}
	}
	n.gen++
	return nil
}

func (n *naive) statuses() (map[string]Status, error) {
	nodes, err := n.expandAll()
	if err != nil {
		return nil, err
	}
	out := map[string]Status{}
	for _, nd := range nodes {
		switch {
		case nd.repo == nil || nd.pin == nil:
			out[nd.path] = StatusDangling
		default:
			wt, ok := n.ws[nd.path]
			switch {
			case !ok:
				out[nd.path] = StatusMissing
			case wt.Dirty:
				out[nd.path] = StatusDirty
			case wt.Checkout != nd.rec.Pinned:
				out[nd.path] = StatusDiverged
			default:
				out[nd.path] = StatusClean
			}
		}
	}
	return out, nil
}

func errKind(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidParam):
		return "invalid-param"
	case errors.Is(err, ErrCycle):
		return "cycle"
	case errors.Is(err, ErrPathConflict):
		return "path-conflict"
	case errors.Is(err, ErrRepoNotFound):
		return "repo-not-found"
	case errors.Is(err, ErrBranchNotFound):
		return "branch-not-found"
	case errors.Is(err, ErrDanglingPin):
		return "dangling-pin"
	case errors.Is(err, ErrDirty):
		return "dirty"
	case errors.Is(err, ErrNonFastForward):
		return "non-fast-forward"
	default:
		return fmt.Sprintf("unknown(%v)", err)
	}
}

// randomStore 生成随机仓库集合：随机提交图、随机分支、随机嵌套子模块表
// （含偶发的悬空固定与缺失仓库引用）。
func randomStore(rng *rand.Rand) (*Store, []RepoID, map[RepoID][]CommitID) {
	store := NewStore()
	repoIDs := []RepoID{"S", "R0", "R1", "R2", "R3"}
	commits := map[RepoID][]CommitID{}
	for _, id := range repoIDs {
		r := NewRepo(id)
		n := 2 + rng.Intn(5)
		var ids []CommitID
		for i := 0; i < n; i++ {
			cid := CommitID(fmt.Sprintf("%s-c%d", id, i))
			var parents []CommitID
			if i > 0 {
				parents = append(parents, ids[rng.Intn(len(ids))])
				if p2 := ids[rng.Intn(len(ids))]; p2 != parents[0] && rng.Intn(3) == 0 {
					parents = append(parents, p2)
				}
			}
			r.AddCommit(&Commit{ID: cid, Parents: parents})
			ids = append(ids, cid)
		}
		r.Branches["main"] = ids[rng.Intn(len(ids))]
		if rng.Intn(2) == 0 {
			r.Branches["dev"] = ids[rng.Intn(len(ids))]
		}
		store.Add(r)
		commits[id] = ids
	}
	for _, id := range repoIDs {
		r := store.Get(id)
		for _, cid := range commits[id] {
			if rng.Intn(2) != 0 {
				continue
			}
			used := map[string]bool{}
			var recs []SubmoduleRecord
			for j := 0; j < rng.Intn(3); j++ {
				p := fmt.Sprintf("s%d", rng.Intn(3))
				if used[p] {
					continue
				}
				used[p] = true
				recs = append(recs, randomRecord(rng, repoIDs, commits, p))
			}
			if tbl, err := NewTable(recs...); err == nil {
				r.Commits[cid].Table = tbl
			}
		}
	}
	return store, repoIDs, commits
}

func randomRecord(rng *rand.Rand, repoIDs []RepoID, commits map[RepoID][]CommitID, path string) SubmoduleRecord {
	tgt := repoIDs[rng.Intn(len(repoIDs))]
	pin := commits[tgt][rng.Intn(len(commits[tgt]))]
	if rng.Intn(12) == 0 {
		pin = "dangling-pin"
	}
	tracking := ""
	switch rng.Intn(6) {
	case 0:
		tracking = "main"
	case 1:
		tracking = "dev"
	case 2:
		tracking = "ghost-branch"
	}
	return SubmoduleRecord{Path: path, Repo: tgt, Pinned: pin, Tracking: tracking}
}

// TestRandomModelComparison 随机挂载树 + 随机操作序列，与朴素对照模型逐步比对。
func TestRandomModelComparison(t *testing.T) {
	for _, seed := range []int64{7, 42, 2026} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			store, repoIDs, commits := randomStore(rng)
			pool := []string{"a", "b", "c", "d", "e", "f"}
			var recs []SubmoduleRecord
			used := map[string]bool{}
			for i := 0; i < rng.Intn(4); i++ {
				p := pool[rng.Intn(len(pool))]
				if used[p] {
					continue
				}
				used[p] = true
				recs = append(recs, randomRecord(rng, repoIDs, commits, p))
			}
			tbl, err := NewTable(recs...)
			if err != nil {
				t.Fatalf("initial table: %v", err)
			}
			store.Get("S").Commits["S-c0"].Table = tbl
			coord, err := NewCoordinator(store, "S", "S-c0")
			if err != nil {
				t.Fatalf("NewCoordinator: %v", err)
			}
			nav := &naive{store: store, super: "S", table: append([]SubmoduleRecord(nil), recs...), ws: map[string]Worktree{}}
			t.Logf("输入=种子 %d 的随机挂载树（初始根表 %d 条）与 300 个随机操作", seed, len(recs))
			for op := 0; op < 300; op++ {
				var cerr, nerr error
				desc := ""
				switch rng.Intn(6) {
				case 0:
					cerr, nerr = coord.Align(), nav.align()
					desc = "Align()"
				case 1:
					cerr, nerr = coord.Advance(), nav.advance()
					desc = "Advance()"
				case 2:
					p := pool[rng.Intn(len(pool))]
					if rng.Intn(6) == 0 {
						p += "/sub"
					}
					rec := randomRecord(rng, repoIDs, commits, p)
					if rng.Intn(15) == 0 {
						rec.Repo = "ghost"
					}
					cerr = coord.AddMount(p, rec.Repo, rec.Pinned, rec.Tracking)
					nerr = nav.add(p, rec.Repo, rec.Pinned, rec.Tracking)
					desc = fmt.Sprintf("AddMount(%q,%s,%s,%q)", p, rec.Repo, rec.Pinned, rec.Tracking)
				case 3:
					p := pool[rng.Intn(len(pool))]
					force := rng.Intn(2) == 0
					cerr, nerr = coord.RemoveMount(p, force), nav.remove(p, force)
					desc = fmt.Sprintf("RemoveMount(%q,force=%v)", p, force)
				case 4:
					p := pool[rng.Intn(len(pool))]
					if st, err := coord.Statuses(); err == nil && len(st) > 0 && rng.Intn(2) == 0 {
						keys := make([]string, 0, len(st))
						for k := range st {
							keys = append(keys, k)
						}
						p = keys[rng.Intn(len(keys))]
					}
					dirty := rng.Intn(3) == 0
					var co CommitID
					if rng.Intn(4) != 0 {
						tgt := repoIDs[rng.Intn(len(repoIDs))]
						co = commits[tgt][rng.Intn(len(commits[tgt]))]
					}
					cerr = coord.SetCheckout(p, co, dirty)
					if co == "" {
						delete(nav.ws, p)
					} else {
						nav.ws[p] = Worktree{Checkout: co, Dirty: dirty}
					}
					desc = fmt.Sprintf("SetCheckout(%q,%q,dirty=%v)", p, co, dirty)
				case 5:
					tgt := repoIDs[rng.Intn(len(repoIDs))]
					br := "main"
					if rng.Intn(3) == 0 {
						br = "dev"
					}
					tip := commits[tgt][rng.Intn(len(commits[tgt]))]
					store.Get(tgt).Branches[br] = tip
					desc = fmt.Sprintf("MoveBranch(%s,%s,%s)", tgt, br, tip)
				}
				if errKind(cerr) != errKind(nerr) {
					t.Fatalf("op %d %s: 协调器=%s 朴素模型=%s", op, desc, errKind(cerr), errKind(nerr))
				}
				if coord.Generation() != nav.gen {
					t.Fatalf("op %d %s: gen %d != %d", op, desc, coord.Generation(), nav.gen)
				}
				cst, cstErr := coord.Statuses()
				nst, nstErr := nav.statuses()
				if errKind(cstErr) != errKind(nstErr) {
					t.Fatalf("op %d %s: Statuses err %s != %s", op, desc, errKind(cstErr), errKind(nstErr))
				}
				if cstErr == nil && !reflect.DeepEqual(cst, nst) {
					t.Fatalf("op %d %s: 状态不一致\n协调器=%v\n朴素模型=%v", op, desc, cst, nst)
				}
				if cstErr == nil {
					for p := range cst {
						cwt, cok := coord.Checkout(p)
						nwt, nok := nav.ws[p]
						if cok != nok || (cok && cwt != nwt) {
							t.Fatalf("op %d %s: 检出 %q 不一致 %v,%v != %v,%v", op, desc, p, cwt, cok, nwt, nok)
						}
					}
				}
				coord.mu.RLock()
				rootTbl := coord.headTable()
				if len(rootTbl) != len(nav.table) {
					t.Fatalf("op %d %s: 根表规模 %d != %d", op, desc, len(rootTbl), len(nav.table))
				}
				for _, r := range nav.table {
					if rootTbl[r.Path] != r {
						t.Fatalf("op %d %s: 根表记录 %q 不一致 %v != %v", op, desc, r.Path, rootTbl[r.Path], r)
					}
				}
				coord.mu.RUnlock()
				t.Logf("op=%03d %s 实际输出=err:%s gen:%d 挂载点数:%d 判定依据=与朴素模型逐项一致", op, desc, errKind(cerr), nav.gen, len(nst))
			}
		})
	}
}
