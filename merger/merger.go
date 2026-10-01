// Package merger 实现带删除回放的倒排索引段合并器。
package merger

import "sync"

// Doc 是登记一批文档时的输入文档：键与按出现顺序排列的词项序列。
type Doc struct {
	Key   string
	Terms []string
}

// Posting 是某词项在某篇存活文档中的倒排记录。
type Posting struct {
	Doc       int
	TF        int
	Positions []int
}

// Stats 是一个段的统计量。
type Stats struct {
	MaxDoc    int
	NumDocs   int
	TermCount int
}

// ErrCode 标识被拒绝操作的拒绝原因。
type ErrCode int

const (
	ErrInvalidArgument ErrCode = iota + 1
	ErrKeyConflict
	ErrSegmentNotFound
	ErrSegmentBusy
	ErrKeyNotFound
	ErrInvalidHandle
)

// Error 描述一次被拒绝的操作及其原因。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Merger 是段合并器，所有方法均可并发调用。
type Merger struct {
	mu sync.Mutex

	// 下一个段编号（登记与提交各取一个），从 1 开始。
	nextSegID int
	// 下一个合并句柄号，从 1 开始。
	nextHandle int

	// segByID 包含所有在册段（含参与未结束合并的输入段）。
	segByID map[int]*segment
	// locByKey 把每个存活键映射到其当前存活文档所在段与局部编号。
	locByKey map[string]docLoc
	// mergeByHandle 保存所有未结束的合并。
	mergeByHandle map[int]*mergeState
}

type docLoc struct {
	segID int
	local int
}

// docSnap 是 BeginMerge 时刻冻结的一篇文档。
type docSnap struct {
	key      string
	terms    []string
	sourceID int
	local    int
}

// mergeState 是一次未结束的两步合并。
type mergeState struct {
	ids  []int
	docs []docSnap
}

// segment 是一个不可变注册批次：文档集合不变，删除标记可变。
type segment struct {
	id       int
	keys     []string
	terms    [][]string
	deleted  []bool
	numDocs  int
	postings map[string][]Posting
	busy     bool
}

// termAgg 累积单个词项在单篇文档内的词频与位置。
type termAgg struct {
	tf        int
	positions []int
}

// New 创建一个空的合并器。
func New() *Merger {
	return &Merger{
		nextSegID:     1,
		nextHandle:    1,
		segByID:       make(map[int]*segment),
		locByKey:      make(map[string]docLoc),
		mergeByHandle: make(map[int]*mergeState),
	}
}

func newError(code ErrCode, msg string) *Error {
	return &Error{Code: code, Msg: msg}
}

// buildPostings 为一批文档（全部视为存活）构建按词项字节序排列的倒排表。
// 若 deleted 非 nil，则对应文档视为已删除。
func buildPostings(termsPerDoc [][]string, deleted []bool) map[string][]Posting {
	perDoc := make([]map[string]termAgg, len(termsPerDoc))
	termDocs := make(map[string][]int)
	termSet := make(map[string]struct{})
	for i, terms := range termsPerDoc {
		if deleted != nil && deleted[i] {
			continue
		}
		aggs := make(map[string]termAgg)
		for pos, term := range terms {
			agg := aggs[term]
			agg.tf++
			agg.positions = append(agg.positions, pos)
			aggs[term] = agg
		}
		perDoc[i] = aggs
		for term := range aggs {
			if _, ok := termSet[term]; !ok {
				termSet[term] = struct{}{}
			}
			termDocs[term] = append(termDocs[term], i)
		}
	}

	postings := make(map[string][]Posting, len(termSet))
	for term, docs := range termDocs {
		list := make([]Posting, 0, len(docs))
		for _, doc := range docs {
			agg := perDoc[doc][term]
			positions := make([]int, len(agg.positions))
			copy(positions, agg.positions)
			list = append(list, Posting{
				Doc:       doc,
				TF:        agg.tf,
				Positions: positions,
			})
		}
		postings[term] = list
	}
	return postings
}

// Register 登记一批文档并返回新段编号。
func (m *Merger) Register(docs []Doc) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(docs) == 0 {
		return 0, newError(ErrInvalidArgument, "register: empty batch")
	}
	for _, doc := range docs {
		if doc.Key == "" {
			return 0, newError(ErrInvalidArgument, "register: empty key")
		}
		for _, term := range doc.Terms {
			if term == "" {
				return 0, newError(ErrInvalidArgument, "register: empty term")
			}
		}
	}

	seen := make(map[string]struct{}, len(docs))
	for _, doc := range docs {
		if _, dup := seen[doc.Key]; dup {
			return 0, newError(ErrKeyConflict, "register: duplicate key within batch: "+doc.Key)
		}
		seen[doc.Key] = struct{}{}
		if _, live := m.locByKey[doc.Key]; live {
			return 0, newError(ErrKeyConflict, "register: key already live: "+doc.Key)
		}
	}

	id := m.nextSegID
	m.nextSegID++

	keys := make([]string, len(docs))
	termsPerDoc := make([][]string, len(docs))
	for i, doc := range docs {
		keys[i] = doc.Key
		termsPerDoc[i] = append([]string(nil), doc.Terms...)
		m.locByKey[doc.Key] = docLoc{segID: id, local: i}
	}
	postings := buildPostings(termsPerDoc, nil)
	m.segByID[id] = &segment{
		id:       id,
		keys:     keys,
		terms:    termsPerDoc,
		deleted:  make([]bool, len(docs)),
		numDocs:  len(docs),
		postings: postings,
	}
	return id, nil
}

// Delete 按键删除当前存活文档。
func (m *Merger) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	loc, ok := m.locByKey[key]
	if !ok {
		return newError(ErrKeyNotFound, "delete: no live document with key: "+key)
	}
	seg := m.segByID[loc.segID]
	seg.deleted[loc.local] = true
	seg.numDocs--
	for _, term := range seg.terms[loc.local] {
		seg.postings[term] = removeDocFromPostings(seg.postings[term], loc.local)
		if len(seg.postings[term]) == 0 {
			delete(seg.postings, term)
		}
	}
	delete(m.locByKey, key)
	return nil
}

func removeDocFromPostings(list []Posting, doc int) []Posting {
	out := list[:0]
	for _, p := range list {
		if p.Doc != doc {
			out = append(out, p)
		}
	}
	return out
}

// BeginMerge 冻结合并集合并返回句柄号。
func (m *Merger) BeginMerge(ids []int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(ids) < 2 {
		return 0, newError(ErrInvalidArgument, "begin merge: need at least 2 segment ids")
	}
	seenIDs := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seenIDs[id]; dup {
			return 0, newError(ErrInvalidArgument, "begin merge: duplicate segment id")
		}
		seenIDs[id] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := m.segByID[id]; !ok {
			return 0, newError(ErrSegmentNotFound, "begin merge: segment not found")
		}
	}
	for _, id := range ids {
		if m.segByID[id].busy {
			return 0, newError(ErrSegmentBusy, "begin merge: segment already merging")
		}
	}

	snaps := make([]docSnap, 0)
	for _, id := range ids {
		seg := m.segByID[id]
		seg.busy = true
		for local, key := range seg.keys {
			if !seg.deleted[local] {
				snaps = append(snaps, docSnap{
					key:      key,
					terms:    seg.terms[local],
					sourceID: id,
					local:    local,
				})
			}
		}
	}

	handle := m.nextHandle
	m.nextHandle++
	m.mergeByHandle[handle] = &mergeState{ids: append([]int(nil), ids...), docs: snaps}
	return handle, nil
}

// Commit 提交合并，返回新段编号。
func (m *Merger) Commit(handle int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.mergeByHandle[handle]
	if !ok {
		return 0, newError(ErrInvalidHandle, "commit: unknown or finished handle")
	}
	delete(m.mergeByHandle, handle)

	n := len(st.docs)
	keys := make([]string, n)
	termsPerDoc := make([][]string, n)
	deleted := make([]bool, n)
	numDocs := 0
	for i, snap := range st.docs {
		keys[i] = snap.key
		termsPerDoc[i] = append([]string(nil), snap.terms...)
		src := m.segByID[snap.sourceID]
		// 删除回放：以被冻结文档本身当前的删除标记为准。
		if src.deleted[snap.local] {
			deleted[i] = true
		} else {
			numDocs++
		}
	}
	postings := buildPostings(termsPerDoc, deleted)

	newID := m.nextSegID
	m.nextSegID++

	// 移除输入段：解除其仍存活文档（即冻结文档）的键映射。
	for _, id := range st.ids {
		seg := m.segByID[id]
		for local, key := range seg.keys {
			if !seg.deleted[local] {
				delete(m.locByKey, key)
			}
		}
		delete(m.segByID, id)
	}

	m.segByID[newID] = &segment{
		id:       newID,
		keys:     keys,
		terms:    termsPerDoc,
		deleted:  deleted,
		numDocs:  numDocs,
		postings: postings,
	}
	for i, key := range keys {
		if !deleted[i] {
			m.locByKey[key] = docLoc{segID: newID, local: i}
		}
	}
	return newID, nil
}

// Abort 放弃合并。
func (m *Merger) Abort(handle int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, ok := m.mergeByHandle[handle]
	if !ok {
		return newError(ErrInvalidHandle, "abort: unknown or finished handle")
	}
	delete(m.mergeByHandle, handle)
	for _, id := range st.ids {
		m.segByID[id].busy = false
	}
	return nil
}

// Stats 返回段统计量的副本。
func (m *Merger) Stats(id int) (Stats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	seg, ok := m.segByID[id]
	if !ok {
		return Stats{}, newError(ErrSegmentNotFound, "stats: segment not found")
	}
	return Stats{
		MaxDoc:    len(seg.keys),
		NumDocs:   seg.numDocs,
		TermCount: len(seg.postings),
	}, nil
}

// Postings 返回某词项倒排表的副本。
func (m *Merger) Postings(id int, term string) ([]Posting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	seg, ok := m.segByID[id]
	if !ok {
		return nil, newError(ErrSegmentNotFound, "postings: segment not found")
	}
	list := seg.postings[term]
	out := make([]Posting, len(list))
	for i, p := range list {
		out[i] = Posting{Doc: p.Doc, TF: p.TF, Positions: append([]int(nil), p.Positions...)}
	}
	return out, nil
}
