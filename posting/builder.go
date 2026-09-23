package posting

import "sort"

// Builder 收集词元出现并产出有序倒排链。
type Builder struct {
	terms map[string]map[uint32][]uint32
	// DupAdd 统计被拒绝的“同文档同位置同词”重复添加次数。
	DupAdd int
}

// NewBuilder 创建空构建器。
func NewBuilder() *Builder {
	return &Builder{terms: map[string]map[uint32][]uint32{}}
}

// Add 记录 term 在文档 doc 的位置 pos（0-based）。
// 同一 (doc,pos,term) 重复添加会被拒绝并累加 DupAdd。
func (b *Builder) Add(term string, doc, pos uint32) bool {
	docs, ok := b.terms[term]
	if !ok {
		docs = map[uint32][]uint32{}
		b.terms[term] = docs
	}
	positions, ok := docs[doc]
	if ok {
		for _, p := range positions {
			if p == pos {
				b.DupAdd++
				return false
			}
		}
	}
	docs[doc] = append(docs[doc], pos)
	return true
}

// Build 产出文档号、位置均有序的链，按词元字典序排列。
func (b *Builder) Build() []*Chain {
	names := make([]string, 0, len(b.terms))
	for term := range b.terms {
		names = append(names, term)
	}
	sort.Strings(names)
	chains := make([]*Chain, 0, len(names))
	for _, term := range names {
		docs := b.terms[term]
		docIDs := make([]uint32, 0, len(docs))
		for id := range docs {
			docIDs = append(docIDs, id)
		}
		sort.Slice(docIDs, func(i, j int) bool { return docIDs[i] < docIDs[j] })
		chain := &Chain{Term: term, Docs: make([]Doc, 0, len(docIDs))}
		for _, id := range docIDs {
			positions := append([]uint32(nil), docs[id]...)
			sort.Slice(positions, func(i, j int) bool { return positions[i] < positions[j] })
			chain.Docs = append(chain.Docs, Doc{ID: id, Positions: positions})
		}
		chains = append(chains, chain)
	}
	return chains
}

// MergeChains 将同一词的多条有序链归并为一条（文档与位置均去重）。
func MergeChains(term string, parts []*Chain) *Chain {
	cursors := make([]int, len(parts))
	out := &Chain{Term: term}
	for {
		best := uint32(0)
		found := false
		for i, p := range parts {
			if cursors[i] >= len(p.Docs) {
				continue
			}
			id := p.Docs[cursors[i]].ID
			if !found || id < best {
				best, found = id, true
			}
		}
		if !found {
			return out
		}
		var collected []uint32
		for i, p := range parts {
			if cursors[i] < len(p.Docs) && p.Docs[cursors[i]].ID == best {
				collected = append(collected, p.Docs[cursors[i]].Positions...)
				cursors[i]++
			}
		}
		out.Docs = append(out.Docs, Doc{ID: best, Positions: dedupSort(collected)})
	}
}

func dedupSort(in []uint32) []uint32 {
	sort.Slice(in, func(i, j int) bool { return in[i] < in[j] })
	out := in[:0]
	for _, v := range in {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return out
}
