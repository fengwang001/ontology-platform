// Package mailthread 实现邮件线程归并器：按 References 链、In-Reply-To
// 与回复主题回退规则把乱序到达的邮件归入线程，支持桥接合并与线程根更换，
// 使线程划分、线程编号与合并事件可精确复现。
package mailthread

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// 参数取值范围。
const (
	MaxWindow = int64(1e9)  // W 的最大值（秒）
	MaxNodes  = 1e5         // N 的最大值
	MaxTS     = int64(1e12) // ts 的最大值（秒）
	MaxRefs   = 50          // refs 的最大项数
)

// 错误分类：拒绝按此顺序只报第一个。
var (
	// ErrInvalidParam 参数非法（构造参数越界、id 为空、ts 越界、
	// refs 超 50 项、refs 或 inReplyTo 含空串、refs 或 inReplyTo 含 id 自身）。
	ErrInvalidParam = errors.New("mailthread: invalid parameter")
	// ErrDuplicate 重复登记（id 此前已是真实邮件）。
	ErrDuplicate = errors.New("mailthread: duplicate message id")
	// ErrCapacity 容量不足（现有节点数加新增节点个数大于 N）。
	ErrCapacity = errors.New("mailthread: node capacity exceeded")
	// ErrNotFound 查询的节点或线程不存在。
	ErrNotFound = errors.New("mailthread: not found")
)

// Path 表示 Add 的归并途径。
type Path int

const (
	// PathRef 走规则（1）：L 非空或 id 此前是占位，按引用链并入/合并。
	PathRef Path = iota
	// PathSubject 走规则（2）：回复主题回退选中候选线程。
	PathSubject
	// PathNew 走规则（3）：新建只含该邮件的线程。
	PathNew
)

func (p Path) String() string {
	switch p {
	case PathRef:
		return "ref"
	case PathSubject:
		return "subject"
	case PathNew:
		return "new"
	}
	return "unknown"
}

// Result 是 Add 的返回结果。
type Result struct {
	// Thread 为结果线程编号（根的 id）。
	Thread string
	// Path 为归并途径。
	Path Path
	// Gone 为本次涉及的操作前线程编号中不等于结果编号者，字节序升序。
	Gone []string
}

// Member 是线程的真实成员。
type Member struct {
	ID string
	TS int64
}

// node 是线程图中的一个节点：真实邮件或占位。
type node struct {
	id   string
	real bool
	ts   int64
	norm string // 规范化主题（仅真实邮件有效）
	th   *thread
}

// thread 是节点的连通块，编号等于根的 id。
type thread struct {
	id      string // 线程编号 == 根的 id
	root    *node  // 真实邮件中 (ts, id) 最小者
	members map[string]*node
	realCnt int
	minTS   int64 // 真实邮件最小 ts
	maxTS   int64 // 真实邮件最大 ts
}

// Threader 是邮件线程归并器，所有方法可并发调用，
// 结果等价于某个串行顺序（内部以互斥锁串行化）。
type Threader struct {
	mu      sync.Mutex
	w       int64
	n       int
	nodes   map[string]*node
	threads map[string]*thread // 线程编号 -> 线程
	// bySubject 按线程根的规范化主题索引线程，使候选查找只考察
	// 同主题线程，与总线程数无关。
	bySubject map[string]map[*thread]struct{}
	// scans 统计候选查找实际考察的线程数（非导出计数器，供测试证明
	// 考察数不超过同主题线程数）。
	scans int64
}

// New 构造归并器。w 为主题回退窗口（0 到 1e9 秒），n 为节点上限（1 到 1e5）。
func New(w int64, n int) (*Threader, error) {
	if w < 0 || w > MaxWindow || n < 1 || n > MaxNodes {
		return nil, ErrInvalidParam
	}
	return &Threader{
		w:         w,
		n:         n,
		nodes:     make(map[string]*node),
		threads:   make(map[string]*thread),
		bySubject: make(map[string]map[*thread]struct{}),
	}, nil
}

// normalizeSubject 反复去掉开头的空白与前缀 re:、fw:、fwd:（不分大小写，
// 前缀与冒号之间不许有空白）以及「回复:」「回复：」「转发:」「转发：」，
// 最后去掉首尾空白。isReply 为真当且仅当至少去掉过一个前缀且结果非空。
func normalizeSubject(s string) (norm string, isReply bool) {
	stripped := false
	for {
		s = strings.TrimLeftFunc(s, unicode.IsSpace)
		if size, ok := matchPrefix(s); ok {
			s = s[size:]
			stripped = true
			continue
		}
		break
	}
	s = strings.TrimFunc(s, unicode.IsSpace)
	return s, stripped && s != ""
}

// matchPrefix 在 s 开头匹配一个回复/转发前缀，返回其字节长度。
func matchPrefix(s string) (int, bool) {
	for _, p := range []string{"fwd:", "re:", "fw:"} {
		if len(s) >= len(p) && strings.EqualFold(s[:len(p)], p) {
			return len(p), true
		}
	}
	for _, p := range []string{"回复:", "回复：", "转发:", "转发："} {
		if strings.HasPrefix(s, p) {
			return len(p), true
		}
	}
	return 0, false
}

// Add 登记一封邮件，返回线程编号、途径与 Gone。
// 被拒绝的操作不改变任何节点与线程。
func (t *Threader) Add(id string, refs []string, inReplyTo string, subject string, ts int64) (Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// 1. 参数非法。
	if id == "" || ts < 0 || ts > MaxTS || len(refs) > MaxRefs {
		return Result{}, ErrInvalidParam
	}
	seen := make(map[string]struct{}, len(refs)+1)
	refsL := make([]string, 0, len(refs)+1)
	for _, r := range refs {
		if r == "" || r == id {
			return Result{}, ErrInvalidParam
		}
		if _, dup := seen[r]; !dup {
			seen[r] = struct{}{}
			refsL = append(refsL, r)
		}
	}
	if inReplyTo != "" {
		if inReplyTo == id {
			return Result{}, ErrInvalidParam
		}
		if _, dup := seen[inReplyTo]; !dup {
			seen[inReplyTo] = struct{}{}
			refsL = append(refsL, inReplyTo)
		}
	}

	// 2. 重复登记。
	if nd, ok := t.nodes[id]; ok && nd.real {
		return Result{}, ErrDuplicate
	}

	// 3. 容量不足（恰等通过）。
	newCnt := 0
	if _, ok := t.nodes[id]; !ok {
		newCnt++
	}
	for _, r := range refsL {
		if _, ok := t.nodes[r]; !ok {
			newCnt++
		}
	}
	if len(t.nodes)+newCnt > t.n {
		return Result{}, ErrCapacity
	}

	norm, isReply := normalizeSubject(subject)

	// 规则（1）：L 非空，或 id 此前是占位。
	if len(refsL) > 0 || t.nodes[id] != nil {
		return t.addByRef(id, refsL, norm, ts), nil
	}

	// 规则（2）：回复主题回退。
	if isReply {
		if best := t.findCandidate(norm, ts); best != nil {
			return t.joinCandidate(best, id, norm, ts), nil
		}
	}

	// 规则（3）：新建线程。
	nd := &node{id: id, real: true, ts: ts, norm: norm}
	th := &thread{
		id:      id,
		root:    nd,
		members: map[string]*node{id: nd},
		realCnt: 1,
		minTS:   ts,
		maxTS:   ts,
	}
	nd.th = th
	t.nodes[id] = nd
	t.threads[id] = th
	t.indexAdd(th)
	return Result{Thread: id, Path: PathNew}, nil
}

// addByRef 实现规则（1）：把 id 与 L 的全部节点并入同一线程。
func (t *Threader) addByRef(id string, refsL []string, norm string, ts int64) Result {
	involved := make(map[*thread]struct{})
	idNode, ok := t.nodes[id]
	if ok {
		involved[idNode.th] = struct{}{}
	} else {
		idNode = &node{id: id}
		t.nodes[id] = idNode
	}
	refNodes := make([]*node, 0, len(refsL))
	for _, r := range refsL {
		if nd, ok := t.nodes[r]; ok {
			involved[nd.th] = struct{}{}
			refNodes = append(refNodes, nd)
		} else {
			nd := &node{id: r}
			t.nodes[r] = nd
			refNodes = append(refNodes, nd)
		}
	}

	// 记录操作前线程编号，并把涉及线程移出主题索引。
	goneSet := make(map[string]struct{})
	for th := range involved {
		goneSet[th.id] = struct{}{}
		t.indexRemove(th)
	}

	// 选成员最多的线程作为幸存者（按小并大），其余线程逐成员搬入。
	var surv *thread
	for th := range involved {
		if surv == nil || len(th.members) > len(surv.members) ||
			(len(th.members) == len(surv.members) && th.id < surv.id) {
			surv = th
		}
	}
	if surv == nil {
		surv = &thread{members: make(map[string]*node)}
	}
	for th := range involved {
		if th == surv {
			continue
		}
		for nid, nd := range th.members {
			nd.th = surv
			surv.members[nid] = nd
		}
		surv.realCnt += th.realCnt
		if th.realCnt > 0 {
			surv.minTS = min(surv.minTS, th.minTS)
			surv.maxTS = max(surv.maxTS, th.maxTS)
			surv.root = minRoot(surv.root, th.root)
		}
		delete(t.threads, th.id)
		th.members = nil
	}

	// 放入本次新增节点。
	for _, nd := range append(refNodes, idNode) {
		if nd.th == nil {
			nd.th = surv
			surv.members[nd.id] = nd
		}
	}

	// id 登记为真实邮件。
	if !idNode.real {
		idNode.real = true
		idNode.ts = ts
		idNode.norm = norm
		surv.realCnt++
		if surv.realCnt == 1 {
			surv.minTS, surv.maxTS = ts, ts
			surv.root = idNode
		} else {
			surv.minTS = min(surv.minTS, ts)
			surv.maxTS = max(surv.maxTS, ts)
			surv.root = minRoot(surv.root, idNode)
		}
	}

	// 根可能变化，线程编号随之改变。
	oldID := surv.id
	surv.id = surv.root.id
	if surv.id != oldID {
		delete(t.threads, oldID)
	}
	t.threads[surv.id] = surv
	t.indexAdd(surv)

	// Gone：涉及的操作前线程编号中不等于结果编号者。
	delete(goneSet, surv.id)
	var gone []string
	for g := range goneSet {
		gone = append(gone, g)
	}
	sort.Strings(gone)
	return Result{Thread: surv.id, Path: PathRef, Gone: gone}
}

// findCandidate 在同主题线程中按规则（2）选择候选线程。
func (t *Threader) findCandidate(norm string, ts int64) *thread {
	var best *thread
	for th := range t.bySubject[norm] {
		t.scans++
		if th.minTS > ts || ts-th.maxTS > t.w {
			continue
		}
		if best == nil || th.maxTS > best.maxTS ||
			(th.maxTS == best.maxTS && th.id < best.id) {
			best = th
		}
	}
	return best
}

// joinCandidate 把新邮件并入规则（2）选中的候选线程。
func (t *Threader) joinCandidate(th *thread, id, norm string, ts int64) Result {
	oldID := th.id
	t.indexRemove(th)

	nd := &node{id: id, real: true, ts: ts, norm: norm, th: th}
	t.nodes[id] = nd
	th.members[id] = nd
	th.realCnt++
	th.minTS = min(th.minTS, ts)
	th.maxTS = max(th.maxTS, ts)
	th.root = minRoot(th.root, nd)

	th.id = th.root.id
	if th.id != oldID {
		delete(t.threads, oldID)
	}
	t.threads[th.id] = th
	t.indexAdd(th)

	var gone []string
	if oldID != th.id {
		gone = []string{oldID}
	}
	return Result{Thread: th.id, Path: PathSubject, Gone: gone}
}

// minRoot 返回 (ts, id) 字节序更小者。
func minRoot(a, b *node) *node {
	if a == nil {
		return b
	}
	if b.ts < a.ts || (b.ts == a.ts && b.id < a.id) {
		return b
	}
	return a
}

func (t *Threader) indexAdd(th *thread) {
	key := th.root.norm
	bucket := t.bySubject[key]
	if bucket == nil {
		bucket = make(map[*thread]struct{})
		t.bySubject[key] = bucket
	}
	bucket[th] = struct{}{}
}

func (t *Threader) indexRemove(th *thread) {
	key := th.root.norm
	if bucket := t.bySubject[key]; bucket != nil {
		delete(bucket, th)
		if len(bucket) == 0 {
			delete(t.bySubject, key)
		}
	}
}

// ThreadOf 返回 id（真实或占位）所属线程的编号。
func (t *Threader) ThreadOf(id string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	nd, ok := t.nodes[id]
	if !ok {
		return "", ErrNotFound
	}
	return nd.th.id, nil
}

// Members 返回线程的真实成员，按 (ts, id) 升序。
func (t *Threader) Members(threadID string) ([]Member, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	th, ok := t.threads[threadID]
	if !ok {
		return nil, ErrNotFound
	}
	ms := make([]Member, 0, th.realCnt)
	for _, nd := range th.members {
		if nd.real {
			ms = append(ms, Member{ID: nd.id, TS: nd.ts})
		}
	}
	sort.Slice(ms, func(i, j int) bool {
		if ms[i].TS != ms[j].TS {
			return ms[i].TS < ms[j].TS
		}
		return ms[i].ID < ms[j].ID
	})
	return ms, nil
}

// Threads 返回全部线程编号，按字节序升序。
func (t *Threader) Threads() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	ids := make([]string, 0, len(t.threads))
	for id := range t.threads {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
