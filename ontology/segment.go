package ontology

import "sort"

// segment 是一个不可变文档集合；删除只在 deleted 上打标记。
type segment struct {
	id       int
	keys     []string       // 每个局部编号对应的键（含已删除文档）
	terms    [][]string     // 每个局部编号对应的词项序列（含已删除文档）
	deleted  []bool         // 每个局部编号的删除标记
	liveKeys map[string]int // 存活键 -> 局部编号
	busy     bool
}

// frozenDoc 是 BeginMerge 时刻冻结的一篇文档来源。
type frozenDoc struct {
	seg   *segment
	docID int
	key   string
}

// buildSegment 按给定文档序列构造一个新段。
// docs 顺序即局部编号顺序；deleted[i] 决定该编号在新段中是否已删除。
// 词项切片会被深拷贝，避免与调用方共享底层数组。
func buildSegment(id int, docs []frozenDoc, deleted []bool) *segment {
	seg := &segment{
		id:       id,
		keys:     make([]string, len(docs)),
		terms:    make([][]string, len(docs)),
		deleted:  make([]bool, len(docs)),
		liveKeys: make(map[string]int, len(docs)),
	}
	for i, doc := range docs {
		seg.keys[i] = doc.key
		copied := make([]string, len(doc.seg.terms[doc.docID]))
		copy(copied, doc.seg.terms[doc.docID])
		seg.terms[i] = copied
		seg.deleted[i] = deleted[i]
		if !deleted[i] {
			seg.liveKeys[doc.key] = i
		}
	}
	return seg
}

// buildSegmentFromBatch 是登记路径的朴素建段：所有文档均存活。
func buildSegmentFromBatch(id int, batch []Doc) *segment {
	seg := &segment{
		id:       id,
		keys:     make([]string, len(batch)),
		terms:    make([][]string, len(batch)),
		deleted:  make([]bool, len(batch)),
		liveKeys: make(map[string]int, len(batch)),
	}
	for i, doc := range batch {
		seg.keys[i] = doc.Key
		copied := make([]string, len(doc.Terms))
		copy(copied, doc.Terms)
		seg.terms[i] = copied
		seg.liveKeys[doc.Key] = i
	}
	return seg
}

func (s *segment) maxDoc() int { return len(s.terms) }

func (s *segment) numDocs() int { return len(s.liveKeys) }

// termCount 统计存活文档中出现过的不同词项数。
func (s *segment) termCount() int {
	seen := make(map[string]struct{})
	for docID, terms := range s.terms {
		if s.deleted[docID] {
			continue
		}
		for _, term := range terms {
			seen[term] = struct{}{}
		}
	}
	return len(seen)
}

// stats 返回统计量副本。
func (s *segment) stats() Stats {
	return Stats{
		MaxDoc:    s.maxDoc(),
		NumDocs:   s.numDocs(),
		TermCount: s.termCount(),
	}
}

// postings 返回词项在存活文档中按局部编号升序的倒排表副本。
func (s *segment) postings(term string) []Posting {
	result := make([]Posting, 0)
	for docID, terms := range s.terms {
		if s.deleted[docID] {
			continue
		}
		positions := make([]int, 0, len(terms))
		for pos, t := range terms {
			if t == term {
				positions = append(positions, pos)
			}
		}
		if len(positions) > 0 {
			result = append(result, Posting{
				DocID:     docID,
				TF:        len(positions),
				Positions: positions,
			})
		}
	}
	return result
}

// allTerms 按字节序返回存活文档中出现过的全部不同词项。
func (s *segment) allTerms() []string {
	seen := make(map[string]struct{})
	for docID, terms := range s.terms {
		if s.deleted[docID] {
			continue
		}
		for _, term := range terms {
			seen[term] = struct{}{}
		}
	}
	terms := make([]string, 0, len(seen))
	for term := range seen {
		terms = append(terms, term)
	}
	sort.Strings(terms)
	return terms
}
