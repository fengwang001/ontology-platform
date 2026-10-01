package ontology

// 朴素参考模型：完全按照规则文本独立实现，不复用被测代码路径。

type refSeg struct {
	id      int
	keys    []string
	terms   [][]string
	deleted []bool
	busy    bool
}

type refFrozen struct {
	seg   *refSeg
	docID int
}

type refMerge struct {
	id     int
	inputs []int
	docs   []refFrozen
	done   bool
}

type naiveOracle struct {
	nextSeg   int
	nextMerge int
	segments  map[int]*refSeg
	live      map[string]*refSeg
	merges    map[int]*refMerge
}

func newNaiveOracle() *naiveOracle {
	return &naiveOracle{
		nextSeg:   1,
		nextMerge: 1,
		segments:  map[int]*refSeg{},
		live:      map[string]*refSeg{},
		merges:    map[int]*refMerge{},
	}
}

func copyStringSlice(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func (o *naiveOracle) register(batch []Doc) (int, error) {
	if len(batch) == 0 {
		return 0, ErrEmptyBatch
	}
	for _, d := range batch {
		if d.Key == "" {
			return 0, ErrEmptyKey
		}
		for _, term := range d.Terms {
			if term == "" {
				return 0, ErrEmptyTerm
			}
		}
	}
	for _, d := range batch {
		if _, ok := o.live[d.Key]; ok {
			return 0, ErrDuplicateKey
		}
	}
	seenInBatch := map[string]bool{}
	for _, d := range batch {
		if seenInBatch[d.Key] {
			return 0, ErrDuplicateKey
		}
		seenInBatch[d.Key] = true
	}
	id := o.nextSeg
	o.nextSeg++
	seg := &refSeg{id: id, keys: make([]string, len(batch)), terms: make([][]string, len(batch)), deleted: make([]bool, len(batch))}
	for i, d := range batch {
		seg.keys[i] = d.Key
		seg.terms[i] = copyStringSlice(d.Terms)
		o.live[d.Key] = seg
	}
	o.segments[id] = seg
	return id, nil
}

func (o *naiveOracle) delete(key string) error {
	seg, ok := o.live[key]
	if !ok {
		return ErrKeyNotFound
	}
	for docID, k := range seg.keys {
		if k == key && !seg.deleted[docID] {
			seg.deleted[docID] = true
			break
		}
	}
	delete(o.live, key)
	return nil
}

func (o *naiveOracle) beginMerge(ids []int) (int, error) {
	if len(ids) < 2 {
		return 0, ErrInvalidMerge
	}
	seen := map[int]bool{}
	for _, id := range ids {
		if seen[id] {
			return 0, ErrInvalidMerge
		}
		seen[id] = true
	}
	for _, id := range ids {
		if _, ok := o.segments[id]; !ok {
			return 0, ErrSegmentNotFound
		}
	}
	for _, id := range ids {
		if o.segments[id].busy {
			return 0, ErrSegmentBusy
		}
	}
	var frozen []refFrozen
	for _, id := range ids {
		seg := o.segments[id]
		for docID := range seg.keys {
			if !seg.deleted[docID] {
				frozen = append(frozen, refFrozen{seg: seg, docID: docID})
			}
		}
	}
	handle := o.nextMerge
	o.nextMerge++
	o.merges[handle] = &refMerge{id: handle, inputs: append([]int(nil), ids...), docs: frozen}
	for _, id := range ids {
		o.segments[id].busy = true
	}
	return handle, nil
}

func (o *naiveOracle) commit(handle int) (int, error) {
	mg, ok := o.merges[handle]
	if !ok || mg.done {
		return 0, ErrInvalidHandle
	}
	id := o.nextSeg
	o.nextSeg++
	seg := &refSeg{id: id, keys: make([]string, len(mg.docs)), terms: make([][]string, len(mg.docs)), deleted: make([]bool, len(mg.docs))}
	for i, f := range mg.docs {
		seg.keys[i] = f.seg.keys[f.docID]
		seg.terms[i] = copyStringSlice(f.seg.terms[f.docID])
		seg.deleted[i] = f.seg.deleted[f.docID] // 删除回放：以被冻结文档本身此刻标记为准
	}
	for _, inID := range mg.inputs {
		in := o.segments[inID]
		for docID, k := range in.keys {
			if !in.deleted[docID] {
				delete(o.live, k)
			}
		}
		delete(o.segments, inID)
	}
	o.segments[id] = seg
	for docID, k := range seg.keys {
		if !seg.deleted[docID] {
			o.live[k] = seg
		}
	}
	mg.done = true
	return id, nil
}

func (o *naiveOracle) abort(handle int) error {
	mg, ok := o.merges[handle]
	if !ok || mg.done {
		return ErrInvalidHandle
	}
	for _, inID := range mg.inputs {
		if seg, ok := o.segments[inID]; ok {
			seg.busy = false
		}
	}
	mg.done = true
	return nil
}

func refStats(seg *refSeg) Stats {
	numDocs := 0
	termSet := map[string]struct{}{}
	for docID, terms := range seg.terms {
		if seg.deleted[docID] {
			continue
		}
		numDocs++
		for _, t := range terms {
			termSet[t] = struct{}{}
		}
	}
	return Stats{MaxDoc: len(seg.keys), NumDocs: numDocs, TermCount: len(termSet)}
}

func refPostings(seg *refSeg, term string) []Posting {
	out := make([]Posting, 0)
	for docID, terms := range seg.terms {
		if seg.deleted[docID] {
			continue
		}
		var positions []int
		for pos, t := range terms {
			if t == term {
				positions = append(positions, pos)
			}
		}
		if positions != nil {
			out = append(out, Posting{DocID: docID, TF: len(positions), Positions: positions})
		}
	}
	return out
}
