package posting

import (
	"encoding/binary"
	"io"
	"sort"
)

// Doc 是一篇文档内某词的位置集合，位置从 0 开始、升序去重。
type Doc struct {
	ID        uint32
	Positions []uint32
}

// Chain 是一个词元的倒排链：文档号升序，文档内位置升序。
type Chain struct {
	Term string
	Docs []Doc
}

// Builder 增量收集 (term, doc, pos) 三元组，Build 产出有序倒排表。
type Builder struct {
	docs     map[string]map[uint32]map[uint32]struct{}
	DupCount int
}

// NewBuilder 创建空构造器。
func NewBuilder() *Builder {
	return &Builder{docs: map[string]map[uint32]map[uint32]struct{}{}}
}

// Add 记录一次出现；同一文档同一位置重复添加会被拒绝并计数。
func (b *Builder) Add(term string, doc, pos uint32) {
	dm, ok := b.docs[term]
	if !ok {
		dm = map[uint32]map[uint32]struct{}{}
		b.docs[term] = dm
	}
	pm, ok := dm[doc]
	if !ok {
		pm = map[uint32]struct{}{}
		dm[doc] = pm
	}
	if _, dup := pm[pos]; dup {
		b.DupCount++
		return
	}
	pm[pos] = struct{}{}
}

// Build 冻结并返回排序后的倒排索引。
func (b *Builder) Build() *Index {
	idx := &Index{chains: make(map[string]*Chain, len(b.docs)), DupCount: b.DupCount}
	for term, dm := range b.docs {
		docIDs := make([]uint32, 0, len(dm))
		for id := range dm {
			docIDs = append(docIDs, id)
		}
		sort.Slice(docIDs, func(i, j int) bool { return docIDs[i] < docIDs[j] })
		ch := &Chain{Term: term, Docs: make([]Doc, 0, len(docIDs))}
		for _, id := range docIDs {
			posSet := dm[id]
			poss := make([]uint32, 0, len(posSet))
			for p := range posSet {
				poss = append(poss, p)
			}
			sort.Slice(poss, func(i, j int) bool { return poss[i] < poss[j] })
			ch.Docs = append(ch.Docs, Doc{ID: id, Positions: poss})
		}
		idx.chains[term] = ch
	}
	return idx
}

// Index 是不可变的内存倒排表。
type Index struct {
	chains   map[string]*Chain
	DupCount int
}

// Terms 返回全部词元（排序）。
func (x *Index) Terms() []string {
	out := make([]string, 0, len(x.chains))
	for t := range x.chains {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// Chain 返回某词的链；不存在返回 nil。
func (x *Index) Chain(term string) *Chain { return x.chains[term] }

// EncodeChain 对单条链做增量编码并写入 w。
func EncodeChain(w io.Writer, c *Chain) error {
	var buf [binary.MaxVarintLen64]byte
	put := func(v uint64) error {
		n := binary.PutUvarint(buf[:], v)
		_, err := w.Write(buf[:n])
		return err
	}
	if err := put(uint64(len(c.Docs))); err != nil {
		return err
	}
	var prevDoc uint32
	for _, d := range c.Docs {
		if err := put(uint64(d.ID - prevDoc)); err != nil {
			return err
		}
		prevDoc = d.ID
		if err := put(uint64(len(d.Positions))); err != nil {
			return err
		}
		var prevPos uint32
		for _, p := range d.Positions {
			if err := put(uint64(p - prevPos)); err != nil {
				return err
			}
			prevPos = p
		}
	}
	return nil
}

// DecodeChain 从 r 解码一条链（文档数由流首的 uvarint 给出）。
func DecodeChain(r io.ByteReader, term string) (*Chain, int, error) {
	readU := func() (uint64, error) { return binary.ReadUvarint(r) }
	nc, err := readU()
	if err != nil {
		return nil, 0, err
	}
	n := int(nc)
	ch := &Chain{Term: term, Docs: make([]Doc, 0, n)}
	var prevDoc uint32
	for i := 0; i < n; i++ {
		dd, err := readU()
		if err != nil {
			return nil, 0, err
		}
		np, err := readU()
		if err != nil {
			return nil, 0, err
		}
		docID := prevDoc + uint32(dd)
		prevDoc = docID
		poss := make([]uint32, 0, np)
		var prevPos uint32
		for j := uint64(0); j < np; j++ {
			pd, err := readU()
			if err != nil {
				return nil, 0, err
			}
			pos := prevPos + uint32(pd)
			prevPos = pos
			poss = append(poss, pos)
		}
		ch.Docs = append(ch.Docs, Doc{ID: docID, Positions: poss})
	}
	return ch, n, nil
}

// DocGE 返回链中文档号 >= doc 的第一个下标；不存在返回 len(Docs)。
func (c *Chain) DocGE(doc uint32) int {
	return sort.Search(len(c.Docs), func(i int) bool { return c.Docs[i].ID >= doc })
}
