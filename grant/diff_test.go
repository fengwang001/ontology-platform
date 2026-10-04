package grant

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/territory"
)

// naiveGrant 是朴素模型中的一条授权：覆盖集直接展开为叶集合，外加原始时间窗。
type naiveGrant struct {
	id, title, licensee string
	leaves              map[string]bool
	start, end          int64
	exclusive           bool
	deleted             bool
}

// naiveModel 按叶逐枚举重放同一批操作，作为规格参照。
type naiveModel struct {
	tree   *territory.Tree
	maxNow int64
	byID   map[string]*naiveGrant
	log    *strings.Builder
}

func newNaive(tr *territory.Tree) *naiveModel {
	return &naiveModel{tree: tr, byID: map[string]*naiveGrant{}, log: &strings.Builder{}}
}

type opKind int

const (
	opAdd opKind = iota
	opRevoke
	opExtend
)

type op struct {
	kind                      opKind
	now                       int64
	id, title, licensee, node string
	excludes                  []string
	start, end, newEnd        int64
	exclusive                 bool
	queryLeaf                 string
	queryT                    int64
}

func (m *naiveModel) coverLeaves(node string, excludes []string) map[string]bool {
	base, _ := m.tree.LeafRange(node)
	all := m.tree.Leaves()
	out := map[string]bool{}
	for i := base.Lo; i < base.Hi; i++ {
		out[all[i]] = true
	}
	for _, ex := range excludes {
		r, _ := m.tree.LeafRange(ex)
		for i := r.Lo; i < r.Hi; i++ {
			delete(out, all[i])
		}
	}
	return out
}

func (m *naiveModel) timeOK(t int64) bool { return t >= 0 && t <= MaxTime }

func (m *naiveModel) validExcludes(node string, excludes []string) (unknown, bad bool) {
	if !m.tree.Has(node) {
		return false, false
	}
	for _, ex := range excludes {
		if !m.tree.Has(ex) {
			return true, false
		}
	}
	for _, ex := range excludes {
		if !m.tree.IsProperDescendant(ex, node) {
			return false, true
		}
	}
	for i := range excludes {
		for j := i + 1; j < len(excludes); j++ {
			if m.tree.IsProperDescendant(excludes[i], excludes[j]) ||
				m.tree.IsProperDescendant(excludes[j], excludes[i]) {
				return false, true
			}
		}
	}
	return false, false
}

func (m *naiveModel) findConflict(cand *naiveGrant, skip string) *naiveGrant {
	var winner *naiveGrant
	for _, g := range m.byID {
		if g.deleted || g.id == skip || g.title != cand.title {
			continue
		}
		if g.licensee == cand.licensee {
			continue
		}
		if !g.exclusive && !cand.exclusive {
			continue
		}
		if g.start >= cand.end || cand.start >= g.end {
			continue
		}
		shared := false
		for leaf := range cand.leaves {
			if g.leaves[leaf] {
				shared = true
				break
			}
		}
		if shared && (winner == nil || g.id < winner.id) {
			winner = g
		}
	}
	return winner
}

func (m *naiveModel) add(o op) error {
	fmt.Fprintf(m.log, "  NAIVE Add(now=%d id=%s title=%s who=%s node=%s ex=%v [%d,%d) exl=%v)\n",
		o.now, o.id, o.title, o.licensee, o.node, o.excludes, o.start, o.end, o.exclusive)
	switch {
	case !m.timeOK(o.now) || !m.timeOK(o.start) || !m.timeOK(o.end) || o.start >= o.end ||
		o.id == "" || o.title == "" || o.licensee == "" || o.node == "" ||
		len(o.excludes) > territory.MaxExclude || hasDupString(o.excludes):
		fmt.Fprintf(m.log, "    -> %v (invalid argument)\n", ErrInvalidArgument)
		return ErrInvalidArgument
	}
	if o.now < m.maxNow {
		fmt.Fprintf(m.log, "    -> %v\n", ErrClockMovedBack)
		return ErrClockMovedBack
	}
	if _, ok := m.byID[o.id]; ok {
		fmt.Fprintf(m.log, "    -> %v\n", ErrDuplicateID)
		return ErrDuplicateID
	}
	if !m.tree.Has(o.node) {
		fmt.Fprintf(m.log, "    -> %v (node)\n", ErrUnknownNode)
		return ErrUnknownNode
	}
	for _, ex := range o.excludes {
		if !m.tree.Has(ex) {
			fmt.Fprintf(m.log, "    -> %v (exclude %s)\n", ErrUnknownNode, ex)
			return ErrUnknownNode
		}
	}
	if unknown, bad := m.validExcludes(o.node, o.excludes); unknown || bad {
		fmt.Fprintf(m.log, "    -> %v\n", ErrInvalidExclude)
		return ErrInvalidExclude
	}
	leaves := m.coverLeaves(o.node, o.excludes)
	if len(leaves) == 0 {
		fmt.Fprintf(m.log, "    -> %v\n", ErrEmptyCoverage)
		return ErrEmptyCoverage
	}
	cand := &naiveGrant{id: o.id, title: o.title, licensee: o.licensee,
		leaves: leaves, start: o.start, end: o.end, exclusive: o.exclusive}
	if c := m.findConflict(cand, ""); c != nil {
		fmt.Fprintf(m.log, "    -> conflict with %s\n", c.id)
		return &conflictError{g: &Grant{ID: []byte(c.id)}}
	}
	m.byID[o.id] = cand
	m.maxNow = o.now
	fmt.Fprintf(m.log, "    -> accepted, cover=%d leaves\n", len(leaves))
	return nil
}

func (m *naiveModel) revoke(o op) error {
	fmt.Fprintf(m.log, "  NAIVE Revoke(now=%d id=%s)\n", o.now, o.id)
	if !m.timeOK(o.now) || o.id == "" {
		return ErrInvalidArgument
	}
	if o.now < m.maxNow {
		return ErrClockMovedBack
	}
	g, ok := m.byID[o.id]
	if !ok {
		return ErrNotFound
	}
	if o.now >= g.end {
		return ErrExpired
	}
	if o.now <= g.start {
		delete(m.byID, o.id)
		g.deleted = true
		fmt.Fprintf(m.log, "    -> deleted entirely\n")
	} else {
		g.end = o.now
		fmt.Fprintf(m.log, "    -> truncated end=%d\n", g.end)
	}
	m.maxNow = o.now
	return nil
}

func (m *naiveModel) extend(o op) error {
	fmt.Fprintf(m.log, "  NAIVE Extend(now=%d id=%s newEnd=%d)\n", o.now, o.id, o.newEnd)
	if !m.timeOK(o.now) || !m.timeOK(o.newEnd) || o.id == "" {
		return ErrInvalidArgument
	}
	if o.now < m.maxNow {
		return ErrClockMovedBack
	}
	g, ok := m.byID[o.id]
	if !ok {
		return ErrNotFound
	}
	if g.end <= o.now {
		return ErrExpired
	}
	if !(o.newEnd > g.end) {
		return ErrNotExtended
	}
	cand := *g
	cand.end = o.newEnd
	if c := m.findConflict(&cand, g.id); c != nil {
		return &conflictError{g: &Grant{ID: []byte(c.id)}}
	}
	g.end = o.newEnd
	m.maxNow = o.now
	return nil
}

func (m *naiveModel) holders(title, leaf string, t int64) []string {
	var ids []string
	for _, g := range m.byID {
		if !g.deleted && g.title == title && g.leaves[leaf] && g.start <= t && t < g.end {
			ids = append(ids, g.id)
		}
	}
	sort.Strings(ids)
	return ids
}

// randomTree 生成一棵随机树：深度上限 5，内部节点 1~4 个子节点，全部内部节点至少 1 子。
func randomTree(rng *rand.Rand) (*territory.Tree, []string) {
	kids := map[string][]string{}
	var leaves []string
	var build func(prefix string, depth int) string
	nodeCounter := 0
	build = func(prefix string, depth int) string {
		code := prefix
		if depth == territory.MaxDepth || (depth > 0 && rng.Intn(3) == 0) {
			leaves = append(leaves, code)
			return code
		}
		n := 1 + rng.Intn(4)
		row := make([]string, 0, n)
		for k := 0; k < n; k++ {
			nodeCounter++
			child := build(fmt.Sprintf("%s-%d", prefix, k), depth+1)
			row = append(row, child)
		}
		sort.Strings(row)
		kids[code] = row
		return code
	}
	root := build(territory.Root, 0)
	if root != territory.Root {
		panic("root code mismatch")
	}
	tr, err := territory.NewTree(kids)
	if err != nil {
		panic(fmt.Sprintf("random tree invalid: %v", err))
	}
	return tr, leaves
}

// pickDescendants 返回 node 的真后代中至多 k 个互不祖先后代的叶（取叶天然合法）。
func pickDescendants(rng *rand.Rand, tr *territory.Tree, node string, allLeaves []string, k int) []string {
	base, _ := tr.LeafRange(node)
	var pool []string
	for i := base.Lo; i < base.Hi; i++ {
		pool = append(pool, allLeaves[i])
	}
	rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	n := rng.Intn(k + 1)
	if n > len(pool) {
		n = len(pool)
	}
	// 不能排除全部叶（保留至少一个），让“覆盖为空”留给专门的随机分支触发
	if n >= len(pool) {
		n = len(pool) - 1
	}
	if n < 0 {
		n = 0
	}
	return pool[:n]
}

func sameErrClass(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	sentinels := []error{
		ErrInvalidArgument, ErrClockMovedBack, ErrDuplicateID, ErrUnknownNode,
		ErrInvalidExclude, ErrEmptyCoverage, ErrNotFound, ErrExpired, ErrNotExtended,
	}
	for _, s := range sentinels {
		if errors.Is(a, s) || errors.Is(b, s) {
			return errors.Is(a, s) && errors.Is(b, s)
		}
	}
	var ca, cb ConflictError
	if errors.As(a, &ca) && errors.As(b, &cb) {
		return bytes.Equal(ca.Conflict().ID, cb.Conflict().ID)
	}
	return false
}

func TestRandomDifferential1500(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	rng := rand.New(rand.NewSource(20261005))
	titles := []string{"T1", "T2"}
	licensees := []string{"A", "B", "C"}
	var totalOps int
	for seq := 0; seq < 1500; seq++ {
		tr, leaves := randomTree(rng)
		r := NewRegistry(tr)
		na := newNaive(tr)
		var sb strings.Builder
		fmt.Fprintf(&sb, "=== sequence %d: %d leaves ===\n", seq, len(leaves))

		nOps := 12 + rng.Intn(24)
		var now int64
		idSeq := 0
		nextID := func() string { return fmt.Sprintf("g%03d", idSeq+1) }

		for step := 0; step < nOps; step++ {
			now += int64(rng.Intn(3))
			mode := rng.Intn(10)
			var o op
			switch {
			case mode < 6:
				o = op{kind: opAdd, now: now,
					id:       nextID(),
					title:    titles[rng.Intn(len(titles))],
					licensee: licensees[rng.Intn(len(licensees))],
					node:     leaves[rng.Intn(len(leaves))],
				}
				// 20% 概率用内部节点 + 排除，制造挖洞覆盖
				if rng.Intn(5) == 0 {
					all := tr.Leaves()
					// 随机挑一个有 >=3 叶的内部节点
					internal := randomInternalNode(rng, tr, all, leaves)
					if internal != "" {
						o.node = internal
						o.excludes = pickDescendants(rng, tr, internal, all, territory.MaxExclude)
						// 偶发触发覆盖为空：排除该节点下全部叶
						if rng.Intn(10) == 0 {
							base, _ := tr.LeafRange(internal)
							o.excludes = append(o.excludes[:0], all[base.Lo:base.Hi]...)
						}
					}
				}
				// 偶发制造非法参数
				o.start = now - int64(rng.Intn(5))
				o.end = o.start + 1 + int64(rng.Intn(20))
				if rng.Intn(15) == 0 {
					o.end = o.start
				}
				if o.start < 0 {
					o.start = 0
				}
				if o.end > MaxTime {
					o.end = MaxTime
				}
				o.exclusive = rng.Intn(2) == 0
				// 偶发重复 id
				if idSeq > 2 && rng.Intn(12) == 0 {
					o.id = fmt.Sprintf("g%03d", 1+rng.Intn(idSeq))
				}
				// 偶发在排除中加入 node 子树之外的合法叶
				if rng.Intn(10) == 0 && o.node != "MARS" {
					if sp, ok := tr.LeafRange(o.node); ok {
						all := tr.Leaves()
						outside := append(append([]string(nil), all[:sp.Lo]...), all[sp.Hi:]...)
						if len(outside) > 0 {
							o.excludes = append(o.excludes, outside[rng.Intn(len(outside))])
						}
					}
				}
				// 偶发未知节点
				if rng.Intn(20) == 0 {
					o.node = "MARS"
				}
				// 偶发时钟回退
				if now > 2 && rng.Intn(15) == 0 {
					o.now = rng.Int63n(now)
				}
			case mode < 8:
				o = op{kind: opRevoke, now: now, id: fmt.Sprintf("g%03d", 1+rng.Intn(idSeq+1))}
				if now > 2 && rng.Intn(15) == 0 {
					o.now = rng.Int63n(now)
				}
			default:
				o = op{kind: opExtend, now: now, id: fmt.Sprintf("g%03d", 1+rng.Intn(idSeq+1)),
					newEnd: now + int64(rng.Intn(10))}
				if now > 2 && rng.Intn(15) == 0 {
					o.now = rng.Int63n(now)
				}
			}

			var gotErr, wantErr error
			fmt.Fprintf(&sb, "[%d] ", step)
			switch o.kind {
			case opAdd:
				fmt.Fprintf(&sb, "REAL Add(now=%d id=%s title=%s who=%s node=%s ex=%d nodes [%d,%d) exl=%v)\n",
					o.now, o.id, o.title, o.licensee, o.node, len(o.excludes), o.start, o.end, o.exclusive)
				_, gotErr = r.Add(o.now, bs(o.id), bs(o.title), bs(o.licensee), o.node,
					append([]string(nil), o.excludes...), o.start, o.end, o.exclusive)
				wantErr = na.add(o)
				if gotErr == nil {
					idSeq++
				}
			case opRevoke:
				fmt.Fprintf(&sb, "REAL Revoke(now=%d id=%s)\n", o.now, o.id)
				gotErr = r.Revoke(o.now, bs(o.id))
				wantErr = na.revoke(o)
			case opExtend:
				fmt.Fprintf(&sb, "REAL Extend(now=%d id=%s newEnd=%d)\n", o.now, o.id, o.newEnd)
				gotErr = r.Extend(o.now, bs(o.id), o.newEnd)
				wantErr = na.extend(o)
			}
			gotName := errName(gotErr)
			wantName := errName(wantErr)
			if gotName != wantName || (gotName == "conflict" && conflictID(gotErr) != conflictID(wantErr)) {
				fmt.Fprintf(&sb, "MISMATCH: real=%v(%s) naive=%v(%s)\n", gotErr, gotName, wantErr, wantName)
				t.Fatalf("seq %d step %d op %+v\ngot=%v want=%v\n%s\nNAIVE LOG:\n%s",
					seq, step, o, gotErr, wantErr, sb.String(), na.log.String())
			}
			fmt.Fprintf(&sb, "    -> %s\n", gotName)
			totalOps++

			// 每步后对随机 title/leaf/t 做 ActiveAt 与 CanPlay 三态对照
			title := titles[rng.Intn(len(titles))]
			leaf := leaves[rng.Intn(len(leaves))]
			at := int64(rng.Intn(30))
			gotHolders := r.ActiveAt(bs(title), bs(leaf), at)
			wantIDs := na.holders(title, leaf, at)
			gotIDs := make([]string, len(gotHolders))
			for i, g := range gotHolders {
				gotIDs[i] = string(g.ID)
			}
			if fmt.Sprint(gotIDs) != fmt.Sprint(wantIDs) {
				t.Fatalf("holders seq=%d step=%d title=%s leaf=%s t=%d: real=%v naive=%v",
					seq, step, title, leaf, at, gotIDs, wantIDs)
			}
			for _, who := range licensees {
				gr := canPlayReal(r, title, who, leaf, at)
				wr := naiveCanPlay(na, title, who, leaf, at)
				if gr != wr {
					t.Fatalf("canplay seq=%d step=%d who=%s leaf=%s t=%d: real=%s naive=%s\nholders=%v",
						seq, step, who, leaf, at, gr, wr, wantIDs)
				}
			}
		}
		if seq < 5 {
			t.Logf("\n%s", sb.String())
		}
	}
	t.Logf("differential replay finished: %d ops across 1500 sequences", totalOps)
}

func randomInternalNode(rng *rand.Rand, tr *territory.Tree, all, leaves []string) string {
	candidates := []string{territory.Root}
	// 收集所有覆盖 >=3 叶的内部节点（抽样内部代码：尝试若干已知前缀不现实，
	// 这里利用树规模小，直接遍历叶名前缀不可行；改为只在根与随机叶祖先中选）
	for _, leaf := range leaves {
		cur := leaf
		for {
			p, ok := tr.Parent(cur)
			if !ok {
				break
			}
			sp, _ := tr.LeafRange(p)
			if sp.Hi-sp.Lo >= 3 {
				candidates = append(candidates, p)
			}
			cur = p
		}
	}
	return candidates[rng.Intn(len(candidates))]
}

func errName(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, ErrClockMovedBack):
		return "clockback"
	case errors.Is(err, ErrDuplicateID):
		return "dupid"
	case errors.Is(err, ErrUnknownNode):
		return "unknown"
	case errors.Is(err, ErrInvalidExclude):
		return "badexclude"
	case errors.Is(err, ErrEmptyCoverage):
		return "empty"
	case errors.Is(err, ErrNotFound):
		return "notfound"
	case errors.Is(err, ErrExpired):
		return "expired"
	case errors.Is(err, ErrNotExtended):
		return "notextended"
	}
	var ce ConflictError
	if errors.As(err, &ce) {
		return "conflict"
	}
	return "other"
}

func conflictID(err error) string {
	var ce ConflictError
	if errors.As(err, &ce) {
		return string(ce.Conflict().ID)
	}
	return ""
}

// canPlayReal 复刻 clearance.Judge 的三态判定（仅用 grant 包内可见接口）。
func canPlayReal(r *Registry, title, who, leaf string, t int64) string {
	active := r.ActiveAt(bs(title), bs(leaf), t)
	var blocking string
	for _, g := range active {
		if string(g.Licensee) == who {
			return "allow:" + string(g.ID)
		}
		if g.Exclusive && (blocking == "" || string(g.ID) < blocking) {
			blocking = string(g.ID)
		}
	}
	if blocking != "" {
		return "exclusive:" + blocking
	}
	return "none"
}

func naiveCanPlay(m *naiveModel, title, who, leaf string, t int64) string {
	var blocking string
	for _, id := range m.holders(title, leaf, t) {
		g := m.byID[id]
		if g.licensee == who {
			return "allow:" + id
		}
		if g.exclusive && (blocking == "" || id < blocking) {
			blocking = id
		}
	}
	if blocking != "" {
		return "exclusive:" + blocking
	}
	return "none"
}

func TestConcurrentAccess(t *testing.T) {
	tr, leaves := randomTree(rand.New(rand.NewSource(7)))
	r := NewRegistry(tr)
	done := make(chan struct{})
	for w := 0; w < 8; w++ {
		go func(w int) {
			defer func() { done <- struct{}{} }()
			rng := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < 200; i++ {
				leaf := leaves[rng.Intn(len(leaves))]
				id := fmt.Sprintf("w%d-%d", w, i)
				_, _ = r.Add(int64(i), bs(id), bs("M"), bs(fmt.Sprintf("L%d", w)),
					leaf, nil, int64(i), int64(i)+5, rng.Intn(2) == 0)
				_ = r.ActiveAt(bs("M"), bs(leaf), int64(i))
				_ = r.Revoke(int64(i)+1, bs(id))
			}
		}(w)
	}
	for w := 0; w < 8; w++ {
		<-done
	}
}
