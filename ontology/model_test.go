package ontology

import (
	"fmt"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// naive 是独立的朴素对照模型：纯 map + 线性扫描，表达式解析用正则独立实现，
// 语义按规格逐条直译，用于与 Service 的索引实现做随机化差分比对。
type naive struct {
	objs       map[string]Object
	refs       map[string]string // 直接引用：全名 -> 对象标识
	syms       map[string]string // 符号引用：全名 -> 全名
	logs       map[string][]string
	namespaces []string
	minAbbrev  int
}

func newNaive(namespaces []string, minAbbrev int) *naive {
	return &naive{
		objs:       make(map[string]Object),
		refs:       make(map[string]string),
		syms:       make(map[string]string),
		logs:       make(map[string][]string),
		namespaces: namespaces,
		minAbbrev:  minAbbrev,
	}
}

func (n *naive) put(o Object) { n.objs[o.ID] = o }

func (n *naive) setRef(name, id string) {
	delete(n.syms, name)
	n.refs[name] = id
	n.logs[name] = append(n.logs[name], id)
}

func (n *naive) setSym(name, target string) {
	delete(n.refs, name)
	n.syms[name] = target
}

// nseg 是朴素模型的导航段。
type nseg struct {
	kind segKind
	n    int
}

var (
	reBase   = regexp.MustCompile(`^[0-9A-Za-z._/-]+`)
	rePeel   = regexp.MustCompile(`^\^\{\}`)
	reTree   = regexp.MustCompile(`^\^\{tree\}`)
	reParent = regexp.MustCompile(`^\^[0-9]*`)
	reAncest = regexp.MustCompile(`^~[0-9]*`)
	reReflog = regexp.MustCompile(`^@\{[0-9]+\}`)
)

func satNum(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v > 1<<30 {
		return 1 << 30
	}
	return v
}

// naiveParse 独立解析表达式；ok=false 表示文法非法。
func naiveParse(s string) (base string, segs []nseg, ok bool) {
	if s == "" {
		return "", nil, false
	}
	base = reBase.FindString(s)
	if base == "" {
		return "", nil, false
	}
	rest := s[len(base):]
	for len(rest) > 0 {
		switch {
		case rePeel.MatchString(rest):
			segs = append(segs, nseg{kind: segPeel})
			rest = rest[len("^{}"):]
		case reTree.MatchString(rest):
			segs = append(segs, nseg{kind: segTree})
			rest = rest[len("^{tree}"):]
		case strings.HasPrefix(rest, "^"):
			m := reParent.FindString(rest)
			num := satNum(strings.TrimPrefix(m, "^"), 1)
			if num == 0 {
				return "", nil, false
			}
			segs = append(segs, nseg{kind: segParent, n: num})
			rest = rest[len(m):]
		case strings.HasPrefix(rest, "~"):
			m := reAncest.FindString(rest)
			segs = append(segs, nseg{kind: segAncestor, n: satNum(strings.TrimPrefix(m, "~"), 1)})
			rest = rest[len(m):]
		case strings.HasPrefix(rest, "@"):
			m := reReflog.FindString(rest)
			if m == "" {
				return "", nil, false
			}
			segs = append(segs, nseg{kind: segReflog, n: satNum(m[2:len(m)-1], 0)})
			rest = rest[len(m):]
		default:
			return "", nil, false
		}
	}
	return base, segs, true
}

func (n *naive) countPrefix(p string) int {
	c := 0
	for id := range n.objs {
		if strings.HasPrefix(id, p) {
			c++
		}
	}
	return c
}

// follow 沿符号引用链走到末端；返回末端全名、对象标识与是否悬空。
func (n *naive) follow(name string) (terminal, id string, dangling bool) {
	cur := name
	for i := 0; i <= len(n.syms)+1; i++ {
		if tgt, ok := n.syms[cur]; ok {
			cur = tgt
			continue
		}
		if id, ok := n.refs[cur]; ok {
			return cur, id, false
		}
		return cur, "", true
	}
	return cur, "", true
}

func (n *naive) hasRef(name string) bool {
	if _, ok := n.refs[name]; ok {
		return true
	}
	_, ok := n.syms[name]
	return ok
}

func (n *naive) peel(o Object) Object {
	for o.Type == TypeTag {
		o = n.objs[o.Target]
	}
	return o
}

// resolve 是朴素解析：线性扫描、逐条直译规格。
func (n *naive) resolve(text string, q Query) (Result, *Error) {
	base, segs, ok := naiveParse(text)
	if !ok {
		return Result{}, &Error{Kind: ErrInvalid, Segment: -1}
	}
	usable := len(base) >= n.minAbbrev && len(base) <= idLen && isHex(base)
	var cur Object
	var refName string
	found, exact, dangling := false, false, false
	var id string
	if n.hasRef(base) {
		found, exact = true, true
		refName, id, dangling = n.follow(base)
	} else {
		for _, ns := range n.namespaces {
			if n.hasRef(ns + base) {
				found = true
				refName, id, dangling = n.follow(ns + base)
				break
			}
		}
	}
	switch {
	case found && dangling:
		return Result{}, &Error{Kind: ErrNotExist, Segment: -1}
	case found:
		if !exact && usable && n.countPrefix(base) > 0 {
			return Result{}, &Error{Kind: ErrRefIDAmbiguous, Segment: -1}
		}
		cur = n.objs[id]
	case usable:
		c := n.countPrefix(base)
		if c == 0 {
			return Result{}, &Error{Kind: ErrNotExist, Segment: -1}
		}
		if c > 1 {
			return Result{}, &Error{Kind: ErrPrefixAmbiguous, Candidates: c, Segment: -1}
		}
		for oid, o := range n.objs {
			if strings.HasPrefix(oid, base) {
				cur = o
			}
		}
	default:
		return Result{}, &Error{Kind: ErrNotExist, Segment: -1}
	}
	for i, sg := range segs {
		switch sg.kind {
		case segParent:
			if cur.Type != TypeCommit {
				return Result{}, &Error{Kind: ErrTypeNotNavigable, Segment: i}
			}
			if sg.n > len(cur.Parents) {
				return Result{}, &Error{Kind: ErrNoSuchParent, Segment: i}
			}
			cur = n.objs[cur.Parents[sg.n-1]]
			refName = ""
		case segAncestor:
			if cur.Type != TypeCommit {
				return Result{}, &Error{Kind: ErrTypeNotNavigable, Segment: i}
			}
			for k := 0; k < sg.n; k++ {
				if len(cur.Parents) == 0 {
					return Result{}, &Error{Kind: ErrBeyondHistory, Segment: i}
				}
				cur = n.objs[cur.Parents[0]]
			}
			refName = ""
		case segReflog:
			if refName == "" {
				return Result{}, &Error{Kind: ErrNotARef, Segment: i}
			}
			log := n.logs[refName]
			if sg.n >= len(log) {
				return Result{}, &Error{Kind: ErrNoSuchLogEntry, Segment: i}
			}
			cur = n.objs[log[len(log)-1-sg.n]]
			refName = ""
		case segPeel:
			cur = n.peel(cur)
			refName = ""
		case segTree:
			if cur.Type != TypeCommit {
				return Result{}, &Error{Kind: ErrTypeNotNavigable, Segment: i}
			}
			cur = n.objs[cur.Tree]
			refName = ""
		}
	}
	if q.Type != TypeAny {
		if cur.Type == TypeTag && q.AutoPeel {
			cur = n.peel(cur)
		}
		if cur.Type != q.Type {
			return Result{}, &Error{Kind: ErrTypeMismatch, Segment: -1}
		}
	}
	return Result{ID: cur.ID, Type: cur.Type, Ref: refName}, nil
}

// shortestAbbrev 是朴素最短缩写：与全部其它对象线性求最大 LCP。
func (n *naive) shortestAbbrev(id string) (string, bool) {
	if _, ok := n.objs[id]; !ok {
		return "", false
	}
	lcp := 0
	for other := range n.objs {
		if other != id {
			if v := lcpLen(id, other); v > lcp {
				lcp = v
			}
		}
	}
	w := lcp + 1
	if w < n.minAbbrev {
		w = n.minAbbrev
	}
	if w > idLen {
		w = idLen
	}
	return id[:w], true
}

// TestRandomizedDifferential 用随机对象库、随机引用与随机表达式，把 Service
// 与独立朴素模型的裁决逐条比对（含错误类别、失败段下标、候选个数）。
func TestRandomizedDifferential(t *testing.T) {
	r := rand.New(rand.NewSource(20241006))
	cfg := Config{MinAbbrev: 4, Namespaces: []string{"refs/heads/", "refs/tags/", "refs/remotes/"}}
	svc := NewService(cfg)
	nv := newNaive(cfg.Namespaces, cfg.MinAbbrev)

	// 对象标识取自小规模前缀池，制造前缀冲突与歧义。
	var pool []string
	for i := 0; i < 12; i++ {
		pool = append(pool, randID(r)[:6])
	}
	used := make(map[string]bool)
	genID := func() string {
		for {
			id := pool[r.Intn(len(pool))] + randID(r)[6:]
			if !used[id] {
				used[id] = true
				return id
			}
		}
	}
	var commits, trees, all []string
	put := func(o Object) {
		t.Helper()
		if err := svc.Put(o); err != nil {
			t.Fatalf("Put(%s %s) failed: %v", o.Type, o.ID, err)
		}
		nv.put(o)
		all = append(all, o.ID)
		switch o.Type {
		case TypeCommit:
			commits = append(commits, o.ID)
		case TypeTree:
			trees = append(trees, o.ID)
		}
	}
	for i := 0; i < 300; i++ {
		id := genID()
		switch {
		case i < 30:
			if i%2 == 0 {
				put(Object{ID: id, Type: TypeTree})
			} else {
				put(Object{ID: id, Type: TypeBlob})
			}
		default:
			switch r.Intn(10) {
			case 0, 1, 2, 3, 4:
				var parents []string
				for k, np := 0, r.Intn(3); k < np; k++ {
					parents = append(parents, commits[r.Intn(len(commits))])
				}
				put(Object{ID: id, Type: TypeCommit, Tree: trees[r.Intn(len(trees))], Parents: parents})
			case 5, 6:
				put(Object{ID: id, Type: TypeTag, Target: all[r.Intn(len(all))]})
			case 7, 8:
				put(Object{ID: id, Type: TypeTree})
			default:
				put(Object{ID: id, Type: TypeBlob})
			}
		}
	}

	// 引用：普通短名 + 取自对象前缀的短名（制造引用与标识歧义）+ 多级符号引用。
	shortNames := []string{"main", "dev", "feature/x", "release-1.0", "hotfix"}
	for i := 0; i < 6; i++ {
		shortNames = append(shortNames, all[r.Intn(len(all))][:4+r.Intn(3)])
	}
	var allNames []string
	setRef := func(name, id string) {
		t.Helper()
		if err := svc.SetRef(name, id); err != nil {
			t.Fatalf("SetRef(%q) failed: %v", name, err)
		}
		nv.setRef(name, id)
	}
	for _, sn := range shortNames {
		full := cfg.Namespaces[r.Intn(len(cfg.Namespaces))] + sn
		if r.Intn(4) == 0 {
			full = sn // 直接作为全名
		}
		allNames = append(allNames, full)
		for k, times := 0, 1+r.Intn(3); k < times; k++ {
			setRef(full, all[r.Intn(len(all))])
		}
	}
	for i := 0; i < 5; i++ {
		sym := fmt.Sprintf("sym%d", i)
		target := allNames[r.Intn(len(allNames))] // 只指向已存在的名字，保证无环
		if err := svc.SetSymbolic(sym, target); err != nil {
			t.Fatalf("SetSymbolic(%q) failed: %v", sym, err)
		}
		nv.setSym(sym, target)
		allNames = append(allNames, sym)
	}

	genBase := func() string {
		switch r.Intn(9) {
		case 0:
			return shortNames[r.Intn(len(shortNames))]
		case 1:
			return allNames[r.Intn(len(allNames))]
		case 2:
			id := all[r.Intn(len(all))]
			return id[:1+r.Intn(10)]
		case 3:
			return all[r.Intn(len(all))]
		case 4:
			return randID(r)[:1+r.Intn(12)]
		case 5:
			return fmt.Sprintf("name%d", r.Intn(1000))
		case 6:
			return fmt.Sprintf("bad!%d", r.Intn(10))
		case 7:
			return all[r.Intn(len(all))][:1+r.Intn(3)] // 低于下限
		default:
			return ""
		}
	}
	genSeg := func() string {
		switch r.Intn(12) {
		case 0:
			return "^"
		case 1:
			return fmt.Sprintf("^%d", r.Intn(5))
		case 2:
			return "~"
		case 3:
			return fmt.Sprintf("~%d", r.Intn(5))
		case 4:
			return fmt.Sprintf("@{%d}", r.Intn(5))
		case 5:
			return "^{}"
		case 6:
			return "^{tree}"
		case 7:
			return "^0"
		case 8:
			return "^{bogus}"
		case 9:
			return "@{x}"
		case 10:
			return "@"
		default:
			return fmt.Sprintf("~%d", r.Intn(100))
		}
	}

	mismatch := 0
	for i := 0; i < 3000; i++ {
		expr := genBase()
		for k, ns := 0, r.Intn(4); k < ns; k++ {
			expr += genSeg()
		}
		q := Query{Type: ObjType(r.Intn(5)), AutoPeel: r.Intn(2) == 0}
		gotRes, gotErr := svc.Resolve(expr, q)
		wantRes, wantErr := nv.resolve(expr, q)
		ok := (gotErr == nil) == (wantErr == nil)
		if ok && gotErr != nil {
			ok = gotErr.Kind == wantErr.Kind && gotErr.Segment == wantErr.Segment &&
				gotErr.Candidates == wantErr.Candidates
		}
		if ok && gotErr == nil {
			ok = gotRes == wantRes
		}
		desc := fmt.Sprintf("svc=(%+v,%v) naive=(%+v,%v)", gotRes, gotErr, wantRes, wantErr)
		if !ok {
			mismatch++
			t.Errorf("input=%q query=%+v -> MISMATCH %s", expr, q, desc)
			if mismatch > 5 {
				t.Fatal("too many mismatches")
			}
		} else if i%300 == 0 {
			t.Logf("input=%q query=%+v -> %s | 判定依据: 与朴素模型一致", expr, q, desc)
		}
	}
	t.Logf("判定依据: 3000 条随机表达式与朴素模型全部一致，mismatch=%d", mismatch)

	// 最短缩写与朴素模型比对。
	for i := 0; i < 150; i++ {
		id := all[r.Intn(len(all))]
		got, gerr := svc.ShortestAbbrev(id)
		want, ok := nv.shortestAbbrev(id)
		if (gerr == nil) != ok || (ok && got != want) {
			t.Fatalf("ShortestAbbrev(%s) svc=(%q,%v) naive=(%q,%v)", id, got, gerr, want, ok)
		}
		if i%50 == 0 {
			t.Logf("ShortestAbbrev(%s...)=%q | 判定依据: 与朴素线性模型一致", id[:8], got)
		}
	}
}
