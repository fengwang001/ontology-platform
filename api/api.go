// Package api 对外门面：提交、过期、只读视图与自检。依赖 fileref。
package api

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"

	"ontology/fileref"
	"ontology/snapchain"
)

var ErrInvalidParam = errors.New("api: invalid parameter") // 如 Expire 的 N 为负
var ErrNonMonotonicTS = errors.New("api: ts not strictly increasing")
var ErrNameConflict = errors.New("api: file name conflict") // 文件名重复、相交或曾出现过
var ErrNotInCurrent = errors.New("api: remove target not in current")

// API 线程安全的门面：写操作互斥，读操作（Snapshots/Files/SelfCheck）可并发。
type API struct {
	mu    sync.RWMutex
	chain snapchain.Chain
	store fileref.Store
}

func New() *API { return &API{} }

// Commit 提交新快照并返回快照号（S1 起）；所有校验先于任何状态变更，失败不留痕。
func (a *API) Commit(ts int64, add, remove []string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, has := a.chain.Current()
	inCur := map[string]bool{}
	for _, f := range cur.Files {
		inCur[f] = true
	}
	rm := map[string]bool{}
	for _, f := range remove {
		if rm[f] {
			return "", ErrNameConflict
		}
		if !inCur[f] {
			return "", ErrNotInCurrent
		}
		rm[f] = true
	}
	next := make([]string, 0, len(cur.Files)+len(add))
	for _, f := range cur.Files {
		if !rm[f] {
			next = append(next, f)
		}
	}
	seen := map[string]bool{}
	for _, f := range add {
		if seen[f] || rm[f] || a.store.Seen(f) {
			return "", ErrNameConflict
		}
		seen[f] = true
		next = append(next, f)
	}
	if has && ts <= cur.Ts {
		return "", ErrNonMonotonicTS
	}
	sort.Strings(next)
	a.store.Register(add, next)
	return "S" + strconv.Itoa(a.chain.Commit(ts, next)), nil
}

// Expire 过期旧快照并物理删除孤儿文件，返回删除的文件名（升序）。空表幂等。
func (a *API) Expire(n int, t int64) ([]string, error) {
	if n < 0 {
		return nil, ErrInvalidParam
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, expired := a.chain.Expire(n, t)
	return a.store.ApplyExpire(expired), nil
}

func (a *API) Snapshots() []snapchain.Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.chain.Snapshots()
}

func (a *API) Files() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.store.Files()
}

// SelfCheck 对内置八步序列（对照 NOTES.md 推导）与故障注入核验四条不变量。
func (a *API) SelfCheck() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	g := New()
	type step struct {
		exp               bool
		ts, tt            int64
		n                 int
		add, rm           []string
		snaps, del, files string
	}
	steps := []step{
		{ts: 10, add: []string{"f1", "f2"}, snaps: "[{1 10 [f1 f2]}]", files: "[f1 f2]"},
		{ts: 20, add: []string{"f3"}, rm: []string{"f1"}, snaps: "[{1 10 [f1 f2]} {2 20 [f2 f3]}]", files: "[f1 f2 f3]"},
		{ts: 30, add: []string{"f4"}, rm: []string{"f2"}, snaps: "[{1 10 [f1 f2]} {2 20 [f2 f3]} {3 30 [f3 f4]}]", files: "[f1 f2 f3 f4]"},
		{ts: 40, add: []string{"f5"}, rm: []string{"f3"}, snaps: "[{1 10 [f1 f2]} {2 20 [f2 f3]} {3 30 [f3 f4]} {4 40 [f4 f5]}]", files: "[f1 f2 f3 f4 f5]"},
		{exp: true, n: 1, tt: 15, snaps: "[{2 20 [f2 f3]} {3 30 [f3 f4]} {4 40 [f4 f5]}]", del: "[f1]", files: "[f2 f3 f4 f5]"},
		{ts: 50, add: []string{"f6"}, rm: []string{"f4"}, snaps: "[{2 20 [f2 f3]} {3 30 [f3 f4]} {4 40 [f4 f5]} {5 50 [f5 f6]}]", files: "[f2 f3 f4 f5 f6]"},
		{exp: true, n: 2, tt: 35, snaps: "[{4 40 [f4 f5]} {5 50 [f5 f6]}]", del: "[f2 f3]", files: "[f4 f5 f6]"},
		{exp: true, n: 0, tt: 50, snaps: "[{5 50 [f5 f6]}]", del: "[f4]", files: "[f5 f6]"},
	}
	for i, st := range steps {
		if st.exp {
			del, err := g.Expire(st.n, st.tt)
			if err != nil || fmt.Sprint(del) != st.del {
				return fmt.Errorf("selfcheck step %d: del=%v err=%v", i+1, del, err)
			}
		} else if _, err := g.Commit(st.ts, st.add, st.rm); err != nil {
			return fmt.Errorf("selfcheck step %d: %w", i+1, err)
		}
		if fmt.Sprint(g.Snapshots()) != st.snaps || fmt.Sprint(g.Files()) != st.files {
			return fmt.Errorf("selfcheck step %d: snaps=%v files=%v", i+1, g.Snapshots(), g.Files())
		}
	}
	before := fmt.Sprint(g.Snapshots(), g.Files())
	bad := []struct {
		op   func() error
		want error
	}{
		{func() error { _, e := g.Expire(-1, 0); return e }, ErrInvalidParam},
		{func() error { _, e := g.Commit(50, []string{"z1"}, nil); return e }, ErrNonMonotonicTS},
		{func() error { _, e := g.Commit(51, []string{"z1", "z1"}, nil); return e }, ErrNameConflict},
		{func() error { _, e := g.Commit(51, []string{"z2"}, []string{"f1"}); return e }, ErrNotInCurrent},
	}
	for i, b := range bad {
		if err := b.op(); !errors.Is(err, b.want) {
			return fmt.Errorf("selfcheck reject %d: got %v, want %v", i, err, b.want)
		}
	}
	if fmt.Sprint(g.Snapshots(), g.Files()) != before {
		return errors.New("selfcheck: rejection mutated state")
	}
	if _, err := g.Commit(60, []string{"z9"}, nil); err != nil {
		return fmt.Errorf("selfcheck: unusable after rejection: %w", err)
	}
	return nil
}
