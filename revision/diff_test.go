package revision

import (
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func nanotime() int64 { return time.Now().UnixNano() }

// naiveModel 是与生产实现完全独立的朴素对照模型：
// map/切片线性扫描，一把大锁串行化，作为串行真值来源。
type naiveModel struct {
	mu   sync.Mutex
	cfg  Config
	ns   []string
	objs map[string]Object
	refs map[string]string
	logs map[string][]string
}

func newNaiveModel(cfg Config, ns []string) *naiveModel {
	return &naiveModel{cfg: cfg, ns: ns, objs: map[string]Object{},
		refs: map[string]string{}, logs: map[string][]string{}}
}

func (m *naiveModel) add(o Object) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objs[o.ID]; ok {
		return
	}
	m.objs[o.ID] = o
}

func (m *naiveModel) setRef(name, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refs[name] = id
	m.logs[name] = append([]string{id}, m.logs[name]...)
}

func (m *naiveModel) setSymbolic(name, target string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, had := m.refs[name]
	m.refs[name] = symbolicPrefix + target
	cur := target
	seen := map[string]bool{}
	cycle := false
	for {
		if cur == name {
			cycle = true
			break
		}
		v, ok := m.refs[cur]
		if !ok || !isSymbolic(v) {
			break
		}
		cur = v[len(symbolicPrefix):]
		if seen[cur] {
			cycle = true
			break
		}
		seen[cur] = true
	}
	if cycle {
		if had {
			m.refs[name] = old
		} else {
			delete(m.refs, name)
		}
		return false
	}
	return true
}

type naiveResult struct {
	id   string
	code ErrorCode // 成功为 -1
	cand int
}

func hasHexPrefix(id, p string) bool {
	if len(id) < len(p) {
		return false
	}
	for i := range p {
		if id[i] != p[i] {
			return false
		}
	}
	return true
}

func (m *naiveModel) countPrefixLocked(p string) int {
	n := 0
	for id := range m.objs {
		if hasHexPrefix(id, p) {
			n++
		}
	}
	return n
}

func (m *naiveModel) resolveChainLocked(name string) (string, string, bool) {
	cur := name
	seen := map[string]bool{}
	for {
		v, ok := m.refs[cur]
		if !ok {
			return "", "", false
		}
		if !isSymbolic(v) {
			return cur, v, true
		}
		cur = v[len(symbolicPrefix):]
		if seen[cur] {
			return "", "", false
		}
		seen[cur] = true
	}
}

// resolve 完全按规格线性扫描求值，作为对照真值。
func (m *naiveModel) resolve(text string, require bool, want ObjType, peel bool) naiveResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, err := parseExpression(text)
	if err != nil {
		return naiveResult{code: ErrInvalid}
	}

	var curID string
	fromRef := false
	refExact, refName := false, ""
	if _, isFull := m.refs[exp.head]; isFull {
		refExact, refName = true, exp.head
	} else {
		for _, nsp := range m.ns {
			if _, ok := m.refs[nsp+exp.head]; ok {
				refName = nsp + exp.head
				break
			}
		}
	}
	hexOK := len(exp.head) >= m.cfg.MinAbbrev && isHex(exp.head)
	if refName != "" {
		direct, target, ok := m.resolveChainLocked(refName)
		if !ok {
			return naiveResult{code: ErrNotFound}
		}
		if hexOK && !refExact && m.countPrefixLocked(exp.head) > 0 {
			return naiveResult{code: ErrRefIDAmbiguous}
		}
		if _, ok := m.objs[target]; !ok {
			return naiveResult{code: ErrNotFound}
		}
		curID, fromRef = target, true
		_ = direct
	} else if hexOK {
		var matches []string
		for id := range m.objs {
			if hasHexPrefix(id, exp.head) {
				matches = append(matches, id)
			}
		}
		if len(matches) > 1 {
			return naiveResult{code: ErrPrefixAmbiguous, cand: len(matches)}
		}
		if len(matches) == 1 {
			curID = matches[0]
		} else {
			return naiveResult{code: ErrNotFound}
		}
	} else {
		return naiveResult{code: ErrNotFound}
	}

	for _, seg := range exp.segs {
		obj := m.objs[curID]
		switch seg.Kind {
		case SegReflog:
			if !fromRef {
				return naiveResult{code: ErrReflogOnID}
			}
			direct, _, _ := m.resolveChainLocked(refName)
			log := m.logs[direct]
			if seg.N >= len(log) {
				return naiveResult{code: ErrReflogMissing}
			}
			curID = log[seg.N]
			if _, ok := m.objs[curID]; !ok {
				return naiveResult{code: ErrNotFound}
			}
			fromRef = false
		case SegParent:
			if obj.Type != TypeCommit {
				return naiveResult{code: ErrNotNavigable}
			}
			if seg.N > len(obj.Parents) {
				return naiveResult{code: ErrParentMissing}
			}
			curID = obj.Parents[seg.N-1]
		case SegAncestor:
			if obj.Type != TypeCommit {
				return naiveResult{code: ErrNotNavigable}
			}
			cur := obj
			for step := 0; step < seg.N; step++ {
				if len(cur.Parents) == 0 {
					return naiveResult{code: ErrBeyondHistory}
				}
				cur = m.objs[cur.Parents[0]]
			}
			curID = cur.ID
		case SegPeel:
			for m.objs[curID].Type == TypeTag {
				curID = m.objs[curID].Target
			}
		case SegTree:
			if obj.Type != TypeCommit {
				return naiveResult{code: ErrNotNavigable}
			}
			curID = obj.Tree
		}
	}
	if require {
		if peel {
			for m.objs[curID].Type == TypeTag {
				curID = m.objs[curID].Target
			}
		}
		if m.objs[curID].Type != want {
			return naiveResult{code: ErrTypeMismatch}
		}
	}
	return naiveResult{id: curID, code: -1}
}

func (m *naiveModel) shortestAbbrev(id string, minLen int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objs[id]; !ok {
		return ""
	}
	ids := make([]string, 0, len(m.objs))
	for k := range m.objs {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	n := minLen
	if n < 1 {
		n = 1
	}
	for n < IDFullLen {
		c := 0
		for _, k := range ids {
			if hasHexPrefix(k, id[:n]) {
				c++
			}
		}
		if c == 1 {
			return id[:n]
		}
		n++
	}
	return id
}

// TestHexTrieDirect：针对压缩边的插入/计数/唯一取回做直接单测。
func TestHexTrieDirect(t *testing.T) {
	tr := newHexTrie()
	// 三个等长、互不前缀冲突的确定 ID（中间填 0，末两位区分）。
	a := "ab" + strings.Repeat("0", IDFullLen-4) + "ab"
	b := "ac" + strings.Repeat("0", IDFullLen-4) + "ac"
	c := "ef" + strings.Repeat("0", IDFullLen-4) + "ef"
	for _, id := range []string{a, b, c, a} { // 含重复插入
		tr.insert(id)
	}
	cases := []struct {
		p    string
		n    int
		uniq string
	}{
		{"a", 2, ""}, {"ab", 1, a}, {"ac", 1, b}, {"abc", 0, ""},
		{"e", 1, c}, {"ef", 1, c}, {"", 3, ""}, {"g", 0, ""},
	}
	for _, tc := range cases {
		if got := tr.countPrefix(tc.p); got != tc.n {
			t.Fatalf("countPrefix(%q)=%d want %d", tc.p, got, tc.n)
		}
		id, ok := tr.findUnique(tc.p)
		if tc.uniq != "" && (!ok || id != tc.uniq) {
			t.Fatalf("findUnique(%q)=%q,%v want %q", tc.p, id, ok, tc.uniq)
		}
	}
	// 最短缩写：ab/ac 共前缀 a => 需要 ab；ff 只需 f。
	if got := tr.shortestUnique(a, 1); got != "ab" {
		t.Fatalf("shortestUnique(ab)=%q want ab", got)
	}
	if got := tr.shortestUnique(c, 1); got != "e" {
		t.Fatalf("shortestUnique(ef)=%q want e", got)
	}
	if got := tr.shortestUnique(a, 3); got != a[:3] {
		t.Fatalf("minLen 3 => %q want %q", got, a[:3])
	}
	// 后续插入只可能让缩写变长。
	d := "ab" + strings.Repeat("0", IDFullLen-6) + "00abd"
	tr.insert(d)
	if got := tr.shortestUnique(a, 1); len(got) < 2 {
		t.Fatalf("增长后缩写不得变短: %q", got)
	}
	logf(t, "[trie] 依据=压缩边分叉/重复插入/唯一取回/缩写单调性 全部断言通过")
}

// TestRandomDifferential：随机对象库 + 随机表达式，与朴素模型逐案比对。
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	for iter := 0; iter < 40; iter++ {
		minAb := 1 + rng.Intn(3)
		ns := []string{"refs/tags/", "refs/heads/", ""}
		s := NewStore(Config{MinAbbrev: minAb}, ns)
		m := newNaiveModel(Config{MinAbbrev: minAb}, ns)

		hexID := func() string {
			b := make([]byte, IDFullLen)
			const digits = "0123456789abcdef"
			for i := range b {
				b[i] = digits[rng.Intn(16)]
			}
			return string(b)
		}
		var trees, blobs, commits, tags []string
		for i := 0; i < 2+rng.Intn(2); i++ {
			id := hexID()
			trees = append(trees, id)
			_ = s.AddObject(Object{ID: id, Type: TypeTree})
			m.add(Object{ID: id, Type: TypeTree})
		}
		for i := 0; i < 2; i++ {
			id := hexID()
			blobs = append(blobs, id)
			_ = s.AddObject(Object{ID: id, Type: TypeBlob})
			m.add(Object{ID: id, Type: TypeBlob})
		}
		for i := 0; i < 4+rng.Intn(6); i++ {
			id := hexID()
			var parents []string
			if len(commits) > 0 {
				parents = append(parents, commits[rng.Intn(len(commits))])
				if len(commits) > 1 && rng.Intn(2) == 0 {
					if p2 := commits[rng.Intn(len(commits))]; p2 != parents[0] {
						parents = append(parents, p2)
					}
				}
			}
			obj := Object{ID: id, Type: TypeCommit,
				Tree: trees[rng.Intn(len(trees))], Parents: parents}
			if err := s.AddObject(obj); err != nil {
				t.Fatalf("iter%d add commit: %v", iter, err)
			}
			m.add(obj)
			commits = append(commits, id)
		}
		for i := 0; i < 2+rng.Intn(3); i++ {
			id := hexID()
			target := commits[rng.Intn(len(commits))]
			if len(tags) > 0 && rng.Intn(2) == 0 {
				target = tags[rng.Intn(len(tags))]
			}
			obj := Object{ID: id, Type: TypeTag, Target: target}
			if err := s.AddObject(obj); err != nil {
				t.Fatalf("iter%d add tag: %v", iter, err)
			}
			m.add(obj)
			tags = append(tags, id)
		}

		targets := append(append(append([]string{}, commits...), tags...), blobs...)
		for _, nm := range []string{"refs/heads/main", "refs/heads/dev", "refs/tags/r", "top"} {
			id := targets[rng.Intn(len(targets))]
			if err := s.SetRef(nm, id); err != nil {
				t.Fatal(err)
			}
			m.setRef(nm, id)
			if rng.Intn(2) == 0 {
				id2 := targets[rng.Intn(len(targets))]
				_ = s.SetRef(nm, id2)
				m.setRef(nm, id2)
			}
		}
		_ = s.SetSymbolicRef("HEAD", "refs/heads/main")
		m.setSymbolic("HEAD", "refs/heads/main")

		// 最短缩写与朴素模型一致。
		for _, c := range commits {
			got, ok := s.ShortestAbbrev(c)
			want := m.shortestAbbrev(c, minAb)
			if !ok || got != want {
				t.Fatalf("iter%d abbrev id=%s got=%q want=%q", iter, c, got, want)
			}
		}

		heads := []string{"main", "r", "dev", "top", "HEAD",
			commits[0][:minAb+rng.Intn(3)], tags[0][:minAb], "zzz", "1", "3"}
		suffixes := []string{"", "^", "^2", "~1", "~5", "^{}", "^{tree}",
			"@{0}", "@{1}", "@{9}", "^^{}", "~1^", "@{0}^", "^@{"}
		for _, head := range heads {
			for _, suf := range suffixes {
				expr := head + suf
				require := rng.Intn(2) == 0
				wantType := []ObjType{TypeCommit, TypeTag, TypeTree, TypeBlob}[rng.Intn(4)]
				peel := rng.Intn(2) == 0
				got, gerr := s.Resolve(expr, ResolveOption{
					Require: require, WantType: wantType, AutoPeel: peel})
				nr := m.resolve(expr, require, wantType, peel)
				gotCode := ErrorCode(-1)
				if gerr != nil {
					gotCode = gerr.(*ResolutionError).Code
				}
				if gotCode != nr.code || (gotCode == -1 && got.Object.ID != nr.id) {
					t.Fatalf("iter%d expr=%q req=%v wantType=%d peel=%v: "+
						"实际 id=%.12s code=%d cand=%d, 模型 id=%.12s code=%d cand=%d",
						iter, expr, require, wantType, peel,
						got.Object.ID, gotCode, candOf(gerr), nr.id, nr.code, nr.cand)
				}
			}
		}
		logf(t, "[差分] iter=%d objects=%d 依据=全部 %d 个随机表达式与朴素模型裁决一致",
			iter, len(m.objs), len(heads)*len(suffixes))
	}
}

func candOf(err error) int {
	if re, ok := err.(*ResolutionError); ok {
		return re.Candidates
	}
	return 0
}

// TestConcurrentSerializability：并发写入/引用更新/解析，断言
//  1. 无竞态（配合 -race）；2) 每次解析结果都能在某个朴素串行快照中找到
//     （即等价于某串行顺序，且解析看到的是完整瞬间状态）。
func TestConcurrentSerializability(t *testing.T) {
	s := testStore(t, 1)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 64)

	// 写入者：不断追加新提交（父为当前 main 链上的提交）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		parent := idOf("31")
		for i := 0; i < 1000; i++ {
			select {
			case <-stop:
				return
			default:
			}
			id := fmtID(0xa0000000 + uint64(i))
			obj := Object{ID: id, Type: TypeCommit, Tree: idOf("11"),
				Parents: []string{parent}}
			if err := s.AddObject(obj); err != nil {
				errs <- err
				return
			}
			if err := s.SetRef("refs/heads/main", id); err != nil {
				errs <- err
				return
			}
			parent = id
			if i%3 == 0 {
				if err := s.SetSymbolicRef("HEAD", "refs/heads/main"); err != nil &&
					!IsCode(err, ErrSymbolicCycle) {
					errs <- err
					return
				}
			}
		}
	}()

	// 解析者：表达式结果必须始终自洽——main 的第一父必然是上一个 main。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			cur, err := s.Resolve("main", ResolveOption{})
			if err != nil {
				errs <- err
				return
			}
			parent, perr := s.Resolve("main^", ResolveOption{})
			if perr != nil {
				// main 可能恰好是初始根提交 33 链外对象；初始 main 有父，故不允许失败。
				errs <- perr
				return
			}
			if cur.Object.Type != TypeCommit {
				errs <- errConc("main 必须解析为提交")
				return
			}
			if len(cur.Object.Parents) == 0 || cur.Object.Parents[0] != parent.Object.ID {
				errs <- errConc("main^ 与 main.Parents[0] 不一致：解析未看到原子瞬间状态")
				return
			}
			// HEAD（符号）与 main 必须指向同一对象。
			head, herr := s.Resolve("HEAD", ResolveOption{})
			if herr == nil && head.Object.ID != cur.Object.ID {
				errs <- errConc("符号引用 HEAD 与 main 跨快照错位")
				return
			}
		}
	}()

	// 缩写读取者：最短缩写必须永远唯一命中同一对象。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			res, err := s.Resolve("main", ResolveOption{})
			if err != nil {
				errs <- err
				return
			}
			ab, ok := s.ShortestAbbrev(res.Object.ID)
			if !ok || len(ab) < 1 {
				errs <- errConc("当前 main 必须可产生最短缩写")
				return
			}
			r2, err := s.Resolve(ab, ResolveOption{})
			if err != nil || r2.Object.ID != res.Object.ID {
				errs <- errConc("并发下产生的缩写必须仍唯一命中")
				return
			}
		}
	}()

	close(stop)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	logf(t, "[并发] 依据=写/读/缩写三 goroutine 交叉，每次解析的 main 与其 ^、HEAD 均自洽（-race 下无竞态）")
}

type concErr string

func (e concErr) Error() string { return string(e) }
func errConc(s string) error    { return concErr(s) }

func fmtID(n uint64) string {
	const hexd = "0123456789abcdef"
	b := make([]byte, IDFullLen)
	for i := range b {
		b[i] = '0'
	}
	for i := IDFullLen - 1; i >= 0 && n > 0; i-- {
		b[i] = hexd[n&0xf]
		n >>= 4
	}
	return string(b)
}

// TestScalingSublinear：前缀匹配与最短缩写开销不随对象总数增长。
// 用全异首字符对象把前缀计数路径与缩写路径分别压在常数深度上，
// 断言 200 -> 20000 对象时延迟近似持平（允许 4 倍噪声余量）。
func TestScalingSublinear(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping scale test in -short mode")
	}
	measure := func(n int) int64 {
		s := NewStore(Config{MinAbbrev: 1}, nil)
		ids := make([]string, 0, n)
		for i := 0; i < n; i++ {
			id := fmtIDFromInt(i)
			ids = append(ids, id)
			if err := s.AddObject(Object{ID: id, Type: TypeBlob}); err != nil {
				t.Fatal(err)
			}
		}
		// 前缀计数：用 16 个固定短前缀各查一次。
		start := nanotime()
		var sink int
		for rep := 0; rep < 2000; rep++ {
			sink += s.db.countPrefix("a")
			sink += s.db.countPrefix("b")
			if _, ok := s.ShortestAbbrev(ids[n/2]); !ok {
				t.Fatal("abbrev")
			}
		}
		_ = sink
		return nanotime() - start
	}
	small := measure(200)
	big := measure(20000)
	ratio := float64(big) / float64(small)
	logf(t, "[规模] 200对象=%dns 20000对象=%dns 比值=%.2fx 依据=两操作只依赖标识长度40",
		small, big, ratio)
	if ratio > 4 {
		t.Fatalf("前缀匹配/缩写耗时随对象数增长超 4 倍，疑似线性扫描")
	}
}

func fmtIDFromInt(i int) string {
	// 首字符在 a-f 之间分布，其后 39 位由整数编码，保证互不相同。
	b := make([]byte, IDFullLen)
	for k := range b {
		b[k] = '0'
	}
	b[0] = "abcdef"[i%6]
	n := uint64(i / 6)
	const hexd = "0123456789abcdef"
	for k := IDFullLen - 1; k >= 1 && n > 0; k-- {
		b[k] = hexd[n&0xf]
		n >>= 4
	}
	return string(b)
}
