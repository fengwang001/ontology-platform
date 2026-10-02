// Package ontology 提供带近期点选加成的边输入边搜索（search-as-you-type）
// 查询展开器。
package ontology

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// 可区分的拒绝原因。被拒绝的操作不会改变任何内部状态。
var (
	ErrInvalidArgument = errors.New("ontology: invalid argument")
	ErrDuplicateDoc    = errors.New("ontology: docID already exists")
	ErrDocNotFound     = errors.New("ontology: docID not found")
	ErrTermNotFound    = errors.New("ontology: term not found in any document")
	ErrEmptyQuery      = errors.New("ontology: query is empty")
	ErrQueryTooLong    = errors.New("ontology: query has more than 8 terms")
	ErrInvalidMaxExp   = errors.New("ontology: maxExp out of range")
)

const (
	maxQueryTerms   = 8
	clickWindowSize = 3
	maxMaxExp       = 1000
)

// Candidate 是一个展开候选词项。
type Candidate struct {
	Term   string
	CondDF int // 条件文档频率：条件文档集中包含该词项的文档数
	Score  int // 条件 df + 2×有效点选数
}

// ExpandResult 是一次 Expand 的结果。所有切片均为独立拷贝，不别名内部状态。
type ExpandResult struct {
	Candidates []Candidate
	Truncated  bool
	Hits       []string // 命中文档 docID，按字节序升序
}

type clickRecord struct {
	term string
	c    int64 // 记录点选时的 T
}

// Expander 是并发安全的查询展开器。零值不可用，请用 New。
type Expander struct {
	mu sync.Mutex

	docs map[string]map[string]struct{} // docID -> 去重词项集合
	df   map[string]int                 // 词项 -> 包含它的文档数（全局 df，用于 Pick 存在性检查）

	clicks []clickRecord // 所有点选记录，追加式；失效记录也保留
	t      int64         // 成功 Expand 次数
}

// New 创建一个空的展开器。
func New() *Expander {
	return &Expander{
		docs: map[string]map[string]struct{}{},
		df:   map[string]int{},
	}
}

// Index 登记一篇文档。
func (e *Expander) Index(docID string, terms []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if docID == "" || len(terms) == 0 {
		return ErrInvalidArgument
	}
	set := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		if term == "" || strings.ContainsRune(term, ' ') {
			return ErrInvalidArgument
		}
		set[term] = struct{}{}
	}
	if _, ok := e.docs[docID]; ok {
		return ErrDuplicateDoc
	}
	e.docs[docID] = set
	for term := range set {
		e.df[term]++
	}
	return nil
}

// Remove 删除一篇文档。
func (e *Expander) Remove(docID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	set, ok := e.docs[docID]
	if !ok {
		return ErrDocNotFound
	}
	for term := range set {
		if e.df[term] <= 1 {
			delete(e.df, term)
		} else {
			e.df[term]--
		}
	}
	delete(e.docs, docID)
	return nil
}

// Pick 记录一次点选。
func (e *Expander) Pick(term string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if term == "" || strings.ContainsRune(term, ' ') {
		return ErrInvalidArgument
	}
	if e.df[term] == 0 {
		return ErrTermNotFound
	}
	c := e.t
	for i := len(e.clicks) - 1; i >= 0; i-- {
		if e.clicks[i].c < c {
			break
		}
		if e.clicks[i].term == term {
			return nil // 同一 c 下同一词项只保留一条
		}
	}
	e.clicks = append(e.clicks, clickRecord{term: term, c: c})
	return nil
}

// Expand 解析查询并展开末词前缀。
func (e *Expander) Expand(query string, maxExp int) (ExpandResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	complete, prefix, hasPrefix := parseQuery(query)
	nTerms := len(complete)
	if hasPrefix {
		nTerms++
	}
	if nTerms == 0 {
		return ExpandResult{}, ErrEmptyQuery
	}
	if nTerms > maxQueryTerms {
		return ExpandResult{}, ErrQueryTooLong
	}
	if maxExp < 1 || maxExp > maxMaxExp {
		return ExpandResult{}, ErrInvalidMaxExp
	}

	e.t++
	t := e.t

	// 条件文档集：包含全部（去重后的）完整词的文档；无完整词时为全部文档。
	cond := map[string]map[string]struct{}{}
	if len(complete) == 0 {
		for docID, set := range e.docs {
			cond[docID] = set
		}
	} else {
		needSet := make(map[string]struct{}, len(complete))
		for _, w := range complete {
			needSet[w] = struct{}{}
		}
		for docID, set := range e.docs {
			ok := true
			for w := range needSet {
				if _, has := set[w]; !has {
					ok = false
					break
				}
			}
			if ok {
				cond[docID] = set
			}
		}
	}

	if !hasPrefix {
		// 没有前缀词：没有候选，命中文档就是条件文档集。
		hits := sortedDocIDs(cond)
		return ExpandResult{Candidates: []Candidate{}, Truncated: false, Hits: hits}, nil
	}

	// 统计候选词项的条件 df。
	condDF := map[string]int{}
	for _, set := range cond {
		for term := range set {
			if strings.HasPrefix(term, prefix) {
				condDF[term]++
			}
		}
	}

	// 有效点选数：t-c <= 3（即 c >= t-3）。
	activeClicks := map[string]int{}
	minC := t - int64(clickWindowSize)
	for _, r := range e.clicks {
		if r.c >= minC {
			activeClicks[r.term]++
		}
	}

	terms := make([]string, 0, len(condDF))
	for term := range condDF {
		terms = append(terms, term)
	}
	sort.Slice(terms, func(i, j int) bool {
		si := condDF[terms[i]] + 2*activeClicks[terms[i]]
		sj := condDF[terms[j]] + 2*activeClicks[terms[j]]
		if si != sj {
			return si > sj
		}
		return terms[i] < terms[j]
	})

	total := len(terms)
	truncated := total > maxExp
	kept := terms
	if truncated {
		kept = terms[:maxExp]
	}
	cands := make([]Candidate, len(kept))
	keptSet := make(map[string]struct{}, len(kept))
	for i, term := range kept {
		score := condDF[term] + 2*activeClicks[term]
		cands[i] = Candidate{Term: term, CondDF: condDF[term], Score: score}
		keptSet[term] = struct{}{}
	}

	// 命中文档：条件文档集中至少包含一个被保留候选的文档。
	var hits []string
	for docID, set := range cond {
		for term := range keptSet {
			if _, has := set[term]; has {
				hits = append(hits, docID)
				break
			}
		}
	}
	sort.Strings(hits)

	return ExpandResult{Candidates: cands, Truncated: truncated, Hits: hits}, nil
}

func sortedDocIDs(docs map[string]map[string]struct{}) []string {
	ids := make([]string, 0, len(docs))
	for docID := range docs {
		ids = append(ids, docID)
	}
	sort.Strings(ids)
	return ids
}

// parseQuery 按规则解析查询串：0x20 为唯一分隔符，连续空格合并；
// 以空格结尾时全部是完整词，否则最后一个词为前缀词。
func parseQuery(query string) (complete []string, prefix string, hasPrefix bool) {
	words := strings.Split(query, " ")
	tokens := words[:0]
	for _, w := range words {
		if w != "" {
			tokens = append(tokens, w)
		}
	}
	if strings.HasSuffix(query, " ") {
		return tokens, "", false
	}
	if len(tokens) == 0 {
		return nil, "", false
	}
	return tokens[:len(tokens)-1], tokens[len(tokens)-1], true
}
