package pathlock

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveModel 是独立的朴素对照实现：用 map 存锁，全量扫描判定冲突，
// 与服务的 trie/treap 实现完全无关，用于随机操作序列的交叉比对。
type naiveModel struct {
	locks  map[uint64]Lock
	byPath map[string]uint64
	nextID uint64
	audit  int
	admins map[string]bool
}

func newNaiveModel(admins ...string) *naiveModel {
	m := &naiveModel{
		locks:  make(map[uint64]Lock),
		byPath: make(map[string]uint64),
		admins: make(map[string]bool),
	}
	for _, a := range admins {
		m.admins[a] = true
	}
	return m
}

func isAncestor(a, b string) bool { return strings.HasPrefix(b, a+"/") }

func (m *naiveModel) lock(user, path string) (Lock, error) {
	if user == "" {
		return Lock{}, invalidArg("空用户")
	}
	np, err := NormalizePath(path)
	if err != nil {
		return Lock{}, err
	}
	if id, ok := m.byPath[np]; ok {
		l := m.locks[id]
		if l.Owner == user {
			return Lock{}, &Error{Code: ErrHeldBySelf, Holder: user}
		}
		return Lock{}, &Error{Code: ErrHeldByOther, Holder: l.Owner}
	}
	var best *Lock
	for _, l := range m.locks {
		l := l
		if l.Owner == user {
			continue
		}
		if isAncestor(l.Path, np) || isAncestor(np, l.Path) {
			if best == nil || lessPath(l.Path, best.Path) {
				best = &l
			}
		}
	}
	if best != nil {
		return Lock{}, &Error{Code: ErrAncestorOrDescendantConflict, Holder: best.Owner, Conflict: best}
	}
	m.nextID++
	l := Lock{ID: m.nextID, Path: np, Owner: user}
	m.locks[l.ID] = l
	m.byPath[np] = l.ID
	return l, nil
}

func lessPath(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

func (m *naiveModel) unlock(user string, id uint64, force bool) error {
	if user == "" {
		return invalidArg("空用户")
	}
	if force && !m.admins[user] {
		return &Error{Code: ErrPermissionDenied}
	}
	l, ok := m.locks[id]
	if !ok {
		return &Error{Code: ErrLockNotFound}
	}
	if !force && l.Owner != user {
		return &Error{Code: ErrNotOwner, Holder: l.Owner}
	}
	delete(m.locks, id)
	delete(m.byPath, l.Path)
	if force {
		m.audit++
	}
	return nil
}

func (m *naiveModel) validate(user string, paths []string, release bool) (PushResult, error) {
	if user == "" {
		return PushResult{}, invalidArg("空用户")
	}
	if len(paths) == 0 {
		return PushResult{}, invalidArg("空批次")
	}
	var norm []string
	seen := map[string]bool{}
	for _, p := range paths {
		np, err := NormalizePath(p)
		if err != nil {
			return PushResult{}, err
		}
		if !seen[np] {
			seen[np] = true
			norm = append(norm, np)
		}
	}
	conflicts := map[string]string{}
	for _, np := range norm {
		for _, l := range m.locks {
			if l.Owner == user {
				continue
			}
			if l.Path == np || isAncestor(l.Path, np) || isAncestor(np, l.Path) {
				conflicts[l.Path] = l.Owner
			}
		}
	}
	if len(conflicts) > 0 {
		list := make([]PathConflict, 0, len(conflicts))
		for p, h := range conflicts {
			list = append(list, PathConflict{Path: p, Holder: h})
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
		return PushResult{Accepted: false, Conflicts: list}, nil
	}
	res := PushResult{Accepted: true}
	if release {
		for _, np := range norm {
			if id, ok := m.byPath[np]; ok && m.locks[id].Owner == user {
				res.Released = append(res.Released, id)
				delete(m.locks, id)
				delete(m.byPath, np)
			}
		}
	}
	return res, nil
}

func (m *naiveModel) list(prefix string) []Lock {
	var out []Lock
	for _, l := range m.locks {
		if prefix == "" || l.Path == prefix || strings.HasPrefix(l.Path, prefix+"/") {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func randomPath(rng *rand.Rand) string {
	segs := []string{"a", "b", "c", "d", "e", "f"}
	depth := 1 + rng.Intn(4)
	parts := make([]string, 0, depth)
	for i := 0; i < depth; i++ {
		parts = append(parts, segs[rng.Intn(len(segs))])
	}
	p := strings.Join(parts, "/")
	switch rng.Intn(12) {
	case 0:
		p = "/" + p + "/"
	case 1:
		p = strings.Replace(p, "/", "//", 1)
	case 2:
		p = "x/../" + p
	case 3:
		p = "../" + p // 逃出仓库根，非法
	case 4:
		p = p + "\x01" // 不可打印字节，非法
	}
	return p
}

// TestRandomizedAgainstModel 用随机操作序列比对服务与朴素模型，
// 两者的每次调用结果必须完全一致。
func TestRandomizedAgainstModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	s := NewService("admin")
	m := newNaiveModel("admin")
	users := []string{"alice", "bob", "carol", "admin", ""}

	for i := 0; i < 4000; i++ {
		user := users[rng.Intn(len(users))]
		switch rng.Intn(6) {
		case 0, 1, 2: // 加锁
			path := randomPath(rng)
			sl, serr := s.Lock(user, path)
			ml, merr := m.lock(user, path)
			if errCodeOf(serr) != errCodeOf(merr) {
				t.Fatalf("第 %d 步 Lock(%q,%q): 服务 %v 模型 %v", i, user, path, serr, merr)
			}
			if serr == nil && (sl.ID != ml.ID || sl.Path != ml.Path || sl.Owner != ml.Owner) {
				t.Fatalf("第 %d 步 Lock(%q,%q): 服务 %+v 模型 %+v", i, user, path, sl, ml)
			}
			if pe, ok := serr.(*Error); ok && pe.Code == ErrAncestorOrDescendantConflict {
				me := merr.(*Error)
				if pe.Conflict.Path != me.Conflict.Path || pe.Holder != me.Holder {
					t.Fatalf("第 %d 步冲突锁不一致: 服务 %+v 模型 %+v", i, pe.Conflict, me.Conflict)
				}
			}
			if pe, ok := serr.(*Error); ok && pe.Holder != "" {
				if me := merr.(*Error); pe.Holder != me.Holder {
					t.Fatalf("第 %d 步持有者不一致: 服务 %q 模型 %q", i, pe.Holder, me.Holder)
				}
			}
		case 3: // 释放（含强制）
			id := uint64(rng.Intn(int(m.nextID) + 3))
			force := rng.Intn(4) == 0
			serr := s.Unlock(user, id, force)
			merr := m.unlock(user, id, force)
			if errCodeOf(serr) != errCodeOf(merr) {
				t.Fatalf("第 %d 步 Unlock(%q,%d,%v): 服务 %v 模型 %v", i, user, id, force, serr, merr)
			}
			if got := len(s.AuditLog()); got != m.audit {
				t.Fatalf("第 %d 步审计数不一致: 服务 %d 模型 %d", i, got, m.audit)
			}
		case 4: // 推送校验（含顺带释放）
			n := 1 + rng.Intn(4)
			batch := make([]string, n)
			for j := range batch {
				batch[j] = randomPath(rng)
			}
			release := rng.Intn(2) == 0
			sr, serr := s.ValidatePush(user, batch, release)
			mr, merr := m.validate(user, batch, release)
			if errCodeOf(serr) != errCodeOf(merr) {
				t.Fatalf("第 %d 步 ValidatePush: 服务 %v 模型 %v", i, serr, merr)
			}
			if serr == nil {
				if sr.Accepted != mr.Accepted ||
					!equalConflicts(sr.Conflicts, mr.Conflicts) ||
					!equalIDs(sr.Released, mr.Released) {
					t.Fatalf("第 %d 步 ValidatePush(%q,%v,%v): 服务 %+v 模型 %+v",
						i, user, batch, release, sr, mr)
				}
			}
		case 5: // 前缀列举
			prefix := []string{"", "a", "b/c", "d"}[rng.Intn(4)]
			sl, _, _, serr := s.ListByPrefix(prefix, "", 10000)
			if serr != nil {
				t.Fatalf("第 %d 步 ListByPrefix(%q): %v", i, prefix, serr)
			}
			ml := m.list(prefix)
			if len(sl) != len(ml) {
				t.Fatalf("第 %d 步 List(%q): 服务 %d 条 模型 %d 条", i, prefix, len(sl), len(ml))
			}
			for j := range sl {
				if sl[j].ID != ml[j].ID || sl[j].Path != ml[j].Path || sl[j].Owner != ml[j].Owner {
					t.Fatalf("第 %d 步 List(%q) 第 %d 条: 服务 %+v 模型 %+v", i, prefix, j, sl[j], ml[j])
				}
			}
		}
	}
	all, _, _, _ := s.ListByPrefix("", "", 10000)
	t.Logf("输入=4000 步随机操作 实际输出=终态 %d 把锁、审计 %d 条，与模型逐步一致 "+
		"判定依据=服务与朴素模型对同一操作序列的每次应答完全相同", len(all), len(s.AuditLog()))
}

func equalConflicts(a, b []PathConflict) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalIDs(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestPerformanceProof 以插桩计数确定性证明：加锁、释放、单路径校验的
// trie 节点访问数与全库锁总数完全无关；索引比较数仅为对数级增长。
func TestPerformanceProof(t *testing.T) {
	s := NewService()
	fill := func(from, to int) {
		for i := from; i < to; i++ {
			if _, err := s.Lock("seeder", fmt.Sprintf("u/%d/blob", i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	measure := func() (visits, compares int) {
		s.resetStatsForTest()
		l, err := s.Lock("worker", "target/deep/path/file.bin")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ValidatePush("worker", []string{"target/deep/path/other.bin"}, false); err != nil {
			t.Fatal(err)
		}
		if err := s.Unlock("worker", l.ID, false); err != nil {
			t.Fatal(err)
		}
		return s.statsForTest()
	}

	fill(0, 2000)
	v1, c1 := measure()
	fill(2000, 50000)
	v2, c2 := measure()
	t.Logf("输入=锁总数 2000 vs 50000 下的相同加锁+单路径校验+释放 "+
		"实际输出=trie访问 %d vs %d、索引比较 %d vs %d "+
		"判定依据=trie访问严格相等（与总锁数无关），索引比较对数级（线性应为约 25 倍）",
		v1, v2, c1, c2)
	if v1 != v2 {
		t.Errorf("trie 访问数随总锁数变化: %d vs %d", v1, v2)
	}
	if c2 > c1+64 {
		t.Errorf("索引比较数增长超过对数级: %d -> %d", c1, c2)
	}
}
