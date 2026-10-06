package vcs

import (
	"fmt"
	"math/rand"
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// 本文件实现一个独立的朴素对照模型：不缓存、不加锁、直接遍历 map，
// 分类与忽略匹配均为按规格直写的另一份实现，用于随机操作序列的差分比对。

type mConf struct {
	base, ours, theirs *string
}

type model struct {
	snap, idx, wt map[string]string
	conf          map[string]mConf
	ignore        []string
	commits       int
}

func newModel(ignore []string) *model {
	return &model{
		snap:   map[string]string{},
		idx:    map[string]string{},
		wt:     map[string]string{},
		conf:   map[string]mConf{},
		ignore: append([]string(nil), ignore...),
	}
}

func (m *model) known() map[string]bool {
	u := map[string]bool{}
	for p := range m.snap {
		u[p] = true
	}
	for p := range m.idx {
		u[p] = true
	}
	for p := range m.wt {
		u[p] = true
	}
	for p := range m.conf {
		u[p] = true
	}
	return u
}

// mCompatible 朴素 O(n) 祖先后代检查。
func (m *model) mCompatible(p string) bool {
	for q := range m.known() {
		if q == p {
			continue
		}
		if strings.HasPrefix(p, q+"/") || strings.HasPrefix(q, p+"/") {
			return false
		}
	}
	return true
}

// mIgnored 朴素忽略匹配（独立实现，规则同 IgnoreSet）。
func (m *model) mIgnored(p string) bool {
	for _, pat := range m.ignore {
		dir := strings.HasSuffix(pat, "/")
		pat = strings.TrimSuffix(pat, "/")
		if pat == "" {
			continue
		}
		comps := strings.Split(p, "/")
		if strings.Contains(pat, "/") {
			if !dir && globWhole(pat, p) {
				return true
			}
			for i := 1; i < len(comps); i++ {
				if globWhole(pat, strings.Join(comps[:i], "/")) {
					return true
				}
			}
			continue
		}
		lim := len(comps)
		if dir {
			lim--
		}
		for i := 0; i < lim; i++ {
			if globWhole(pat, comps[i]) {
				return true
			}
		}
	}
	return false
}

func globWhole(pat, s string) bool {
	ok, _ := path.Match(pat, s)
	return ok
}

// mStatusOf 朴素分类：按存在性位掩码直写，与 triple.classify 相互独立。
func (m *model) mStatusOf(p string) (PathStatus, bool) {
	if c, ok := m.conf[p]; ok {
		var k ConflictKind
		switch {
		case c.ours != nil && c.theirs != nil && c.base != nil:
			k = ConflictContent
		case c.ours != nil && c.theirs != nil:
			k = ConflictBothAdded
		case c.theirs != nil:
			k = ConflictDeletedByUs
		case c.ours != nil:
			k = ConflictDeletedByThem
		default:
			k = ConflictContent
		}
		return PathStatus{Path: p, Status: StatusConflict, Conflict: k}, true
	}
	s, hs := m.snap[p]
	i, hi := m.idx[p]
	w, hw := m.wt[p]
	if !hs && !hi && !hw {
		return PathStatus{}, false
	}
	mask := 0
	if hs {
		mask |= 4
	}
	if hi {
		mask |= 2
	}
	if hw {
		mask |= 1
	}
	var st Status
	switch mask {
	case 1:
		if m.mIgnored(p) {
			st = StatusIgnored
		} else {
			st = StatusUntracked
		}
	case 2:
		st = StatusStagedModifiedAgain
	case 3:
		if i == w {
			st = StatusStagedAdd
		} else {
			st = StatusStagedModifiedAgain
		}
	case 4:
		st = StatusStagedDeleted
	case 5:
		st = StatusStagedDeleteRecreated
	case 6:
		if s == i {
			st = StatusWorktreeDeleted
		} else {
			st = StatusStagedModifiedAgain
		}
	case 7:
		if s == i {
			if i == w {
				st = StatusUnchanged
			} else {
				st = StatusWorktreeModified
			}
		} else if i == w {
			st = StatusStagedModified
		} else {
			st = StatusStagedModifiedAgain
		}
	}
	return PathStatus{Path: p, Status: st}, true
}

func (m *model) mStatus() []PathStatus {
	list := []PathStatus{}
	for p := range m.known() {
		ps, _ := m.mStatusOf(p)
		if ps.Status != StatusUnchanged {
			list = append(list, ps)
		}
	}
	sort.Slice(list, func(a, b int) bool { return list[a].Path < list[b].Path })
	return list
}

// mStatusOfQuery 复刻 StatusOf：先规范化，再查分类。
func (m *model) mStatusOfQuery(p string) (PathStatus, *Error) {
	n, err := Normalize(p)
	if err != nil {
		return PathStatus{}, err.(*Error)
	}
	ps, ok := m.mStatusOf(n)
	if !ok {
		return PathStatus{}, errf(CodePathNotExist, n, "路径不存在")
	}
	return ps, nil
}

// mResolve 复刻批解析语义：规范化、查重、目录展开、错误优先级。
func (m *model) mResolve(raw []string, universe map[string]bool) ([]string, *Error) {
	if len(raw) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	var norms []string
	for _, r := range raw {
		n, err := Normalize(r)
		if err != nil {
			return nil, err.(*Error)
		}
		if seen[n] {
			return nil, errf(CodeInvalidPath, n, "批内重复路径")
		}
		seen[n] = true
		norms = append(norms, n)
	}
	var errs []*Error
	expanded := map[string]bool{}
	for _, n := range norms {
		if universe[n] {
			expanded[n] = true
			continue
		}
		found := false
		for u := range universe {
			if strings.HasPrefix(u, n+"/") {
				expanded[u] = true
				found = true
			}
		}
		if !found {
			errs = append(errs, errf(CodePathNotExist, n, "路径不存在"))
		}
	}
	if e := prioritize(errs); e != nil {
		return nil, e
	}
	var out []string
	for p := range expanded {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func (m *model) mWrite(p, c string) *Error {
	n, err := Normalize(p)
	if err != nil {
		return err.(*Error)
	}
	if !m.known()[n] && !m.mCompatible(n) {
		return errf(CodeInvalidPath, n, "与已存在路径互为祖先后代")
	}
	m.wt[n] = c
	return nil
}

func (m *model) mDelete(p string) *Error {
	n, err := Normalize(p)
	if err != nil {
		return err.(*Error)
	}
	if _, ok := m.wt[n]; !ok {
		return errf(CodePathNotExist, n, "工作树中不存在")
	}
	delete(m.wt, n)
	return nil
}

func (m *model) mConflict(p string, base, ours, theirs *string) *Error {
	n, err := Normalize(p)
	if err != nil {
		return err.(*Error)
	}
	if base == nil && ours == nil && theirs == nil {
		return errf(CodeInvalidPath, n, "冲突至少需要一个阶段")
	}
	if !m.known()[n] && !m.mCompatible(n) {
		return errf(CodeInvalidPath, n, "与已存在路径互为祖先后代")
	}
	delete(m.idx, n)
	m.conf[n] = mConf{base: base, ours: ours, theirs: theirs}
	return nil
}

func unionKeys(ms ...map[string]string) map[string]bool {
	u := map[string]bool{}
	for _, mm := range ms {
		for p := range mm {
			u[p] = true
		}
	}
	return u
}

func (m *model) mStage(raw []string) *Error {
	u := unionKeys(m.wt, m.idx)
	for p := range m.conf {
		u[p] = true
	}
	paths, e := m.mResolve(raw, u)
	if e != nil {
		return e
	}
	var errs []*Error
	for _, p := range paths {
		if _, ok := m.conf[p]; ok {
			continue
		}
		_, inW := m.wt[p]
		_, inI := m.idx[p]
		if !inW && !inI {
			errs = append(errs, errf(CodePathNotExist, p, "既不在工作树也不在暂存区"))
		}
	}
	if e := prioritize(errs); e != nil {
		return e
	}
	for _, p := range paths {
		if _, ok := m.conf[p]; ok {
			delete(m.conf, p)
			if c, ok := m.wt[p]; ok {
				m.idx[p] = c
			} else {
				delete(m.idx, p)
			}
		} else if c, ok := m.wt[p]; ok {
			m.idx[p] = c
		} else {
			delete(m.idx, p)
		}
	}
	return nil
}

func (m *model) mUnstage(raw []string) *Error {
	u := unionKeys(m.idx, m.snap)
	for p := range m.conf {
		u[p] = true
	}
	paths, e := m.mResolve(raw, u)
	if e != nil {
		return e
	}
	var errs []*Error
	for _, p := range paths {
		if _, ok := m.conf[p]; ok {
			errs = append(errs, errf(CodeInConflict, p, "冲突中不可撤销"))
			continue
		}
		_, inI := m.idx[p]
		_, inS := m.snap[p]
		if !inI && !inS {
			errs = append(errs, errf(CodePathNotExist, p, "既不在暂存区也不在快照"))
		}
	}
	if e := prioritize(errs); e != nil {
		return e
	}
	for _, p := range paths {
		if c, ok := m.snap[p]; ok {
			m.idx[p] = c
		} else {
			delete(m.idx, p)
		}
	}
	return nil
}

func (m *model) mDiscard(raw []string, force bool) *Error {
	u := unionKeys(m.wt, m.idx, m.snap)
	for p := range m.conf {
		u[p] = true
	}
	paths, e := m.mResolve(raw, u)
	if e != nil {
		return e
	}
	var errs []*Error
	for _, p := range paths {
		if _, ok := m.conf[p]; ok {
			errs = append(errs, errf(CodeInConflict, p, "冲突中不可丢弃，请先暂存解决"))
			continue
		}
		_, inW := m.wt[p]
		_, inI := m.idx[p]
		_, inS := m.snap[p]
		if !inW && !inI && !inS {
			errs = append(errs, errf(CodePathNotExist, p, "三份内容中均不存在"))
			continue
		}
		if inW && !inI && !inS && !force {
			errs = append(errs, errf(CodeUntrackedNeedsForce, p, "未跟踪需强制"))
		}
	}
	if e := prioritize(errs); e != nil {
		return e
	}
	for _, p := range paths {
		if c, ok := m.idx[p]; ok {
			m.wt[p] = c
		} else {
			delete(m.wt, p)
		}
	}
	return nil
}

func (m *model) mCommit(allowEmpty bool) *Error {
	if len(m.conf) > 0 {
		var ps []string
		for p := range m.conf {
			ps = append(ps, p)
		}
		sort.Strings(ps)
		return &Error{Code: CodeUnresolvedConflicts, Paths: ps, Detail: "冲突未解决时不允许提交"}
	}
	if !allowEmpty && reflect.DeepEqual(m.idx, m.snap) {
		return errf(CodeNothingToCommit, "", "暂存区与快照一致")
	}
	m.snap = map[string]string{}
	for k, v := range m.idx {
		m.snap[k] = v
	}
	m.commits++
	return nil
}

// TestRandomAgainstModel 随机操作序列：引擎与独立朴素模型逐步比对，
// 每步比较错误类别、整体状态列表、单路径状态与提交序号。
func TestRandomAgainstModel(t *testing.T) {
	paths := []string{"a", "b", "c", "d/a", "d/b", "d/sub/x", "e.log", "f.txt",
		"build/out", "g", "d", "d/sub", "..", "", "a/../b", "d/a"}
	contents := []string{"", "x", "y", "zz"}
	ignore := []string{"*.log", "build/"}

	for _, seed := range []int64{1, 7, 42} {
		rng := rand.New(rand.NewSource(seed))
		ws := New(NewIgnoreSet(ignore...))
		m := newModel(ignore)

		pickPath := func() string { return paths[rng.Intn(len(paths))] }
		pickContent := func() string { return contents[rng.Intn(len(contents))] }
		pickBatch := func() []string {
			n := 1 + rng.Intn(3)
			b := make([]string, n)
			for i := range b {
				b[i] = pickPath()
			}
			return b
		}

		for step := 0; step < 400; step++ {
			var engErr, modErr *Error
			var desc string
			switch rng.Intn(8) {
			case 0:
				p, c := pickPath(), pickContent()
				desc = fmt.Sprintf("WriteFile(%q,%q)", p, c)
				engErr = asErr(ws.WriteFile(p, []byte(c)))
				modErr = m.mWrite(p, c)
			case 1:
				p := pickPath()
				desc = fmt.Sprintf("DeleteFile(%q)", p)
				engErr = asErr(ws.DeleteFile(p))
				modErr = m.mDelete(p)
			case 2:
				b := pickBatch()
				desc = fmt.Sprintf("Stage(%v)", b)
				engErr = asErr(ws.Stage(b))
				modErr = m.mStage(b)
			case 3:
				b := pickBatch()
				desc = fmt.Sprintf("Unstage(%v)", b)
				engErr = asErr(ws.Unstage(b))
				modErr = m.mUnstage(b)
			case 4:
				b := pickBatch()
				f := rng.Intn(2) == 0
				desc = fmt.Sprintf("Discard(%v,%v)", b, f)
				engErr = asErr(ws.Discard(b, f))
				modErr = m.mDiscard(b, f)
			case 5:
				allow := rng.Intn(3) == 0
				desc = fmt.Sprintf("Commit(%v)", allow)
				_, e1 := ws.Commit(allow)
				engErr = asErr(e1)
				modErr = m.mCommit(allow)
			case 6:
				p := pickPath()
				var st ConflictStages
				var mb, mo, mt *string
				if rng.Intn(2) == 0 {
					s := pickContent()
					st.Base, mb = Str(s), &s
				}
				if rng.Intn(2) == 0 {
					s := pickContent()
					st.Ours, mo = Str(s), &s
				}
				if rng.Intn(2) == 0 {
					s := pickContent()
					st.Theirs, mt = Str(s), &s
				}
				desc = fmt.Sprintf("IntroduceConflict(%q)", p)
				engErr = asErr(ws.IntroduceConflict(p, st))
				modErr = m.mConflict(p, mb, mo, mt)
			case 7:
				p := pickPath()
				desc = fmt.Sprintf("StatusOf(%q)", p)
				engPS, e1 := ws.StatusOf(p)
				engErr = asErr(e1)
				modPS, me2 := m.mStatusOfQuery(p)
				modErr = me2
				if engErr == nil && modErr == nil && engPS != modPS {
					t.Fatalf("seed=%d step=%d %s: 单路径状态 引擎=%v 模型=%v", seed, step, desc, engPS, modPS)
				}
			}

			engCode, modCode := codeOf(engErr), codeOf(modErr)
			t.Logf("seed=%d step=%d 输入=%s 实际: 引擎错误=%v 模型错误=%v 判定依据=两者错误类别一致", seed, step, desc, engErr, modErr)
			if engCode != modCode {
				t.Fatalf("seed=%d step=%d %s: 错误类别 引擎=%v 模型=%v", seed, step, desc, engErr, modErr)
			}
			if engCode == CodeUnresolvedConflicts && !reflect.DeepEqual(engErr.Paths, modErr.Paths) {
				t.Fatalf("seed=%d step=%d %s: 未解决列表 引擎=%v 模型=%v", seed, step, desc, engErr.Paths, modErr.Paths)
			}

			engList, modList := ws.Status(), m.mStatus()
			if !reflect.DeepEqual(engList, modList) {
				t.Fatalf("seed=%d step=%d %s: 整体状态不一致\n引擎=%v\n模型=%v", seed, step, desc, engList, modList)
			}
			_, engCommits := ws.Stats()
			if engCommits != m.commits {
				t.Fatalf("seed=%d step=%d %s: 提交序号 引擎=%d 模型=%d", seed, step, desc, engCommits, m.commits)
			}
		}
		t.Logf("seed=%d: 400 步随机序列与朴素模型完全一致", seed)
	}
}

func asErr(err error) *Error {
	if err == nil {
		return nil
	}
	if e, ok := err.(*Error); ok {
		return e
	}
	return &Error{Code: -1, Detail: err.Error()}
}
