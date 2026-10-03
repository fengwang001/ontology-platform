package ontology

import (
	"sort"
	"sync"
)

// AddResult 是 Add 一次登记的结果。
type AddResult struct {
	// ThreadID 是登记后邮件所在线程的编号（线程根的 Message-ID）。
	ThreadID string
	// Way 为 "ref"（引用/id 此前为占位）、"subject"（主题回退命中候选）或 "new"（新建线程）。
	Way string
	// Gone 是本次操作涉及的操作前线程编号中不等于结果编号者，按字节序升序。
	Gone []string
}

const maxRefs = 50

// realMail 保存一封已登记（真实）邮件的不可变信息。
type realMail struct {
	ts int64
}

// comp 是并查集一个 DSU 根（连通块）的汇总信息。
type comp struct {
	// reals 为线程内真实邮件 id 集合（无序，查询时排序）。
	reals map[string]struct{}
	// 线程根：真实邮件中 (ts, id 字节序) 最小者；线程编号即根 id。
	root   string
	rootTS int64
	minTS  int64
	maxTS  int64
	// normSubj 是线程根主题的规范化结果（线程根变化时重算）。
	normSubj string
	size     int // 节点总数（含占位），用于按小并大
}

// Merger 是邮件线程归并器。
type Merger struct {
	mu sync.RWMutex

	w int64 // 主题回退窗口（秒）
	n int   // 节点上限

	parent map[string]string // 并查集
	meta   map[string]*comp  // 仅 DSU 根键有效
	mail   map[string]realMail
	subj   map[string]string // 真实邮件 id -> 原始主题（根重算时使用）

	// bySubject: 规范化主题 -> 以该主题为线程根主题的 DSU 根集合。
	bySubject map[string]map[string]struct{}

	// subjectExamines 是非导出计数器：Add 走主题回退时实际考察的候选线程数。
	// 「考察」指在规范化主题相同的线程中逐一核对时间窗口等条件的次数，
	// 因此只与同主题线程数有关，与线程总数无关。
	subjectExamines int
}

// New 构造主题回退窗口为 w 秒、节点上限为 n 的归并器；参数越界返回错误。
func New(w int64, n int) (*Merger, error) {
	if w < 0 || w > 1_000_000_000 || n < 1 || n > 100_000 {
		return nil, ErrInvalidArgument
	}
	return &Merger{
		w:         w,
		n:         n,
		parent:    make(map[string]string),
		meta:      make(map[string]*comp),
		mail:      make(map[string]realMail),
		subj:      make(map[string]string),
		bySubject: make(map[string]map[string]struct{}),
	}, nil
}

func (m *Merger) find(id string) string {
	root := id
	for m.parent[root] != root {
		root = m.parent[root]
	}
	// 路径压缩。
	for x := id; m.parent[x] != x; {
		next := m.parent[x]
		m.parent[x] = root
		x = next
	}
	return root
}

// findReadOnly 在持读锁时使用：不做路径压缩（不写 map），避免与读锁冲突。
func (m *Merger) findReadOnly(id string) string {
	for m.parent[id] != id {
		id = m.parent[id]
	}
	return id
}

// Add 登记一封邮件并返回归并结果。
func (m *Merger) Add(id string, refs []string, inReplyTo, subject string, ts int64) (AddResult, error) {
	// 参数校验：按规定顺序只报第一个。
	if id == "" {
		return AddResult{}, ErrInvalidArgument
	}
	if ts < 0 || ts > 1_000_000_000_000 {
		return AddResult{}, ErrInvalidArgument
	}
	if len(refs) > maxRefs {
		return AddResult{}, ErrInvalidArgument
	}
	if containsEmpty(refs) {
		return AddResult{}, ErrInvalidArgument
	}
	if inReplyTo != "" && inReplyTo == id {
		return AddResult{}, ErrInvalidArgument
	}
	for _, r := range refs {
		if r == id {
			return AddResult{}, ErrInvalidArgument
		}
	}

	// 引用集 L：refs（去重，重复项视为一项）与非空 inReplyTo 的并集。
	linkSet := dedupePreserving(refs)
	if inReplyTo != "" {
		linkSet = appendUnique(linkSet, inReplyTo)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 重复登记：id 此前已是真实邮件。
	if _, real := m.mail[id]; real {
		return AddResult{}, ErrDuplicate
	}

	_, idExisted := m.parent[id]

	// 容量：现有节点数 + L 与 id 中此前不存在的节点个数 <= N（恰等通过）。
	need := 0
	if !idExisted {
		need++
	}
	for _, r := range linkSet {
		if _, ok := m.parent[r]; !ok {
			need++
		}
	}
	if len(m.parent)+need > m.n {
		return AddResult{}, ErrCapacity
	}

	norm, stripped := normalizeSubject(subject)
	isReply := stripped && norm != ""

	// （1）L 非空，或 id 此前是占位：全部并入同一线程，不再走 (2)(3)。
	if len(linkSet) > 0 || idExisted {
		return m.addByRefs(id, linkSet, subject, norm, ts, idExisted), nil
	}
	// （2）回复主题：在同规范化主题的线程中挑选候选。
	if isReply {
		if cand := m.pickSubjectCandidate(norm, ts); cand != "" {
			return m.addBySubject(id, subject, norm, ts, cand), nil
		}
	}
	// （3）新建线程。
	return m.addNew(id, subject, norm, ts), nil
}

// ThreadOf 返回邮件（真实或占位）所在线程的编号；节点不存在时报错。
func (m *Merger) ThreadOf(id string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.parent[id]; !ok {
		return "", ErrNotFound
	}
	return m.meta[m.findReadOnly(id)].root, nil
}

// Members 返回线程内真实成员，按 (ts, id) 升序；线程不存在时报错。
func (m *Merger) Members(threadID string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.mail[threadID]; !ok {
		return nil, ErrNotFound
	}
	c := m.meta[m.findReadOnly(threadID)]
	if c.root != threadID {
		return nil, ErrNotFound
	}
	ids := make([]string, 0, len(c.reals))
	for id := range c.reals {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ti, tj := m.mail[ids[i]].ts, m.mail[ids[j]].ts
		return ti < tj || (ti == tj && ids[i] < ids[j])
	})
	return ids, nil
}

// Threads 返回全部线程编号（即各线程根的 id），按字节序升序。
func (m *Merger) Threads() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0)
	for dsuRoot, c := range m.meta {
		if m.parent[dsuRoot] == dsuRoot && c.root != "" {
			ids = append(ids, c.root)
		}
	}
	sort.Strings(ids)
	return ids
}

// subjectExamCount 返回非导出的候选考察计数器（测试用，线程安全）。
func (m *Merger) subjectExamCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.subjectExamines
}

// resetSubjectExamCount 清零候选考察计数器（测试用）。
func (m *Merger) resetSubjectExamCount() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subjectExamines = 0
}

func dedupePreserving(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func containsEmpty(list []string) bool {
	for _, s := range list {
		if s == "" {
			return true
		}
	}
	return false
}

// goneList 返回操作前线程编号集合中不等于结果编号者，按字节序升序。
func goneList(preSet map[string]struct{}, result string) []string {
	gone := make([]string, 0, len(preSet))
	for t := range preSet {
		if t != "" && t != result {
			gone = append(gone, t)
		}
	}
	sort.Strings(gone)
	return gone
}
