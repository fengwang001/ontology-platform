package merger

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// oracleSeg 是朴素重建对照模型：每一步都按规则重新组织全部数据。
type oracleSeg struct {
	id       int
	keys     []string
	terms    [][]string
	deleted  []bool
	postings map[string][]Posting
	busy     bool
}

type oracleMerge struct {
	ids  []int
	docs []docSnap
}

// Oracle 是与 Merger 行为对照的独立朴素实现。
type Oracle struct {
	segs     map[int]*oracleSeg
	live     map[string]docLoc
	merges   map[int]*oracleMerge
	nextSeg  int
	nextHand int
}

func NewOracle() *Oracle {
	return &Oracle{
		segs:     map[int]*oracleSeg{},
		live:     map[string]docLoc{},
		merges:   map[int]*oracleMerge{},
		nextSeg:  1,
		nextHand: 1,
	}
}

// oracleBuild 朴素地从文档词项序列与删除标记整体重建倒排表。
func oracleBuild(terms [][]string, deleted []bool) map[string][]Posting {
	type agg struct {
		tf        int
		positions []int
	}
	byTerm := map[string]map[int]agg{}
	for doc, ts := range terms {
		if deleted[doc] {
			continue
		}
		for pos, term := range ts {
			docs, ok := byTerm[term]
			if !ok {
				docs = map[int]agg{}
				byTerm[term] = docs
			}
			cur := docs[doc]
			cur.tf++
			cur.positions = append(cur.positions, pos)
			docs[doc] = cur
		}
	}
	out := make(map[string][]Posting, len(byTerm))
	for term, docs := range byTerm {
		docIDs := make([]int, 0, len(docs))
		for doc := range docs {
			docIDs = append(docIDs, doc)
		}
		sort.Ints(docIDs)
		list := make([]Posting, 0, len(docIDs))
		for _, doc := range docIDs {
			cur := docs[doc]
			list = append(list, Posting{
				Doc:       doc,
				TF:        cur.tf,
				Positions: append([]int(nil), cur.positions...),
			})
		}
		out[term] = list
	}
	return out
}

func (o *Oracle) Register(docs []Doc) (int, ErrCode) {
	if len(docs) == 0 {
		return 0, ErrInvalidArgument
	}
	for _, doc := range docs {
		if doc.Key == "" {
			return 0, ErrInvalidArgument
		}
		for _, term := range doc.Terms {
			if term == "" {
				return 0, ErrInvalidArgument
			}
		}
	}
	seen := map[string]bool{}
	for _, doc := range docs {
		if seen[doc.Key] {
			return 0, ErrKeyConflict
		}
		seen[doc.Key] = true
		if _, ok := o.live[doc.Key]; ok {
			return 0, ErrKeyConflict
		}
	}
	id := o.nextSeg
	o.nextSeg++
	seg := &oracleSeg{
		id:      id,
		keys:    make([]string, len(docs)),
		terms:   make([][]string, len(docs)),
		deleted: make([]bool, len(docs)),
	}
	for i, doc := range docs {
		seg.keys[i] = doc.Key
		seg.terms[i] = append([]string(nil), doc.Terms...)
		o.live[doc.Key] = docLoc{segID: id, local: i}
	}
	seg.postings = oracleBuild(seg.terms, seg.deleted)
	o.segs[id] = seg
	return id, 0
}

func (o *Oracle) Delete(key string) ErrCode {
	loc, ok := o.live[key]
	if !ok {
		return ErrKeyNotFound
	}
	seg := o.segs[loc.segID]
	seg.deleted[loc.local] = true
	seg.postings = oracleBuild(seg.terms, seg.deleted)
	delete(o.live, key)
	return 0
}

func (o *Oracle) BeginMerge(ids []int) (int, ErrCode) {
	if len(ids) < 2 {
		return 0, ErrInvalidArgument
	}
	seen := map[int]bool{}
	for _, id := range ids {
		if seen[id] {
			return 0, ErrInvalidArgument
		}
		seen[id] = true
	}
	for _, id := range ids {
		if _, ok := o.segs[id]; !ok {
			return 0, ErrSegmentNotFound
		}
	}
	for _, id := range ids {
		if o.segs[id].busy {
			return 0, ErrSegmentBusy
		}
	}
	var snaps []docSnap
	for _, id := range ids {
		seg := o.segs[id]
		seg.busy = true
		for local, key := range seg.keys {
			if !seg.deleted[local] {
				snaps = append(snaps, docSnap{
					key:      key,
					terms:    append([]string(nil), seg.terms[local]...),
					sourceID: id,
					local:    local,
				})
			}
		}
	}
	handle := o.nextHand
	o.nextHand++
	o.merges[handle] = &oracleMerge{ids: append([]int(nil), ids...), docs: snaps}
	return handle, 0
}

func (o *Oracle) Commit(handle int) (int, ErrCode) {
	st, ok := o.merges[handle]
	if !ok {
		return 0, ErrInvalidHandle
	}
	delete(o.merges, handle)

	n := len(st.docs)
	keys := make([]string, n)
	terms := make([][]string, n)
	deleted := make([]bool, n)
	for i, snap := range st.docs {
		keys[i] = snap.key
		terms[i] = append([]string(nil), snap.terms...)
		if o.segs[snap.sourceID].deleted[snap.local] {
			deleted[i] = true
		}
	}
	postings := oracleBuild(terms, deleted)

	newID := o.nextSeg
	o.nextSeg++
	for _, id := range st.ids {
		seg := o.segs[id]
		for local, key := range seg.keys {
			if !seg.deleted[local] {
				delete(o.live, key)
			}
		}
		delete(o.segs, id)
	}
	o.segs[newID] = &oracleSeg{
		id:       newID,
		keys:     keys,
		terms:    terms,
		deleted:  deleted,
		postings: postings,
	}
	for i, key := range keys {
		if !deleted[i] {
			o.live[key] = docLoc{segID: newID, local: i}
		}
	}
	return newID, 0
}

func (o *Oracle) Abort(handle int) ErrCode {
	st, ok := o.merges[handle]
	if !ok {
		return ErrInvalidHandle
	}
	delete(o.merges, handle)
	for _, id := range st.ids {
		o.segs[id].busy = false
	}
	return 0
}

func (o *Oracle) Stats(id int) (Stats, ErrCode) {
	seg, ok := o.segs[id]
	if !ok {
		return Stats{}, ErrSegmentNotFound
	}
	numDocs := 0
	for _, d := range seg.deleted {
		if !d {
			numDocs++
		}
	}
	return Stats{MaxDoc: len(seg.keys), NumDocs: numDocs, TermCount: len(seg.postings)}, 0
}

func (o *Oracle) Postings(id int, term string) ([]Posting, ErrCode) {
	seg, ok := o.segs[id]
	if !ok {
		return nil, ErrSegmentNotFound
	}
	list := seg.postings[term]
	out := make([]Posting, len(list))
	for i, p := range list {
		out[i] = Posting{Doc: p.Doc, TF: p.TF, Positions: append([]int(nil), p.Positions...)}
	}
	return out, 0
}

// snapshot 是模型在某个时刻的全部段状态，用于与 Merger 逐字段对照。
type snapshot struct {
	segIDs   []int
	stats    map[int]Stats
	terms    map[int][]string
	postings map[int]map[string][]Posting
	numDocs  int
	liveKeys int
}

func (o *Oracle) Snapshot() snapshot {
	snap := snapshot{
		stats:    map[int]Stats{},
		terms:    map[int][]string{},
		postings: map[int]map[string][]Posting{},
		liveKeys: len(o.live),
	}
	for id, seg := range o.segs {
		snap.segIDs = append(snap.segIDs, id)
		st, _ := o.Stats(id)
		snap.stats[id] = st
		snap.numDocs += st.NumDocs
		ts := make([]string, 0, len(seg.postings))
		posts := make(map[string][]Posting, len(seg.postings))
		for term, list := range seg.postings {
			ts = append(ts, term)
			cp := make([]Posting, len(list))
			for i, p := range list {
				cp[i] = Posting{Doc: p.Doc, TF: p.TF, Positions: append([]int(nil), p.Positions...)}
			}
			posts[term] = cp
		}
		sort.Strings(ts)
		snap.terms[id] = ts
		snap.postings[id] = posts
	}
	sort.Ints(snap.segIDs)
	return snap
}

func (m *Merger) snapshot() snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap := snapshot{
		stats:    map[int]Stats{},
		terms:    map[int][]string{},
		postings: map[int]map[string][]Posting{},
		liveKeys: len(m.locByKey),
	}
	for id, seg := range m.segByID {
		snap.segIDs = append(snap.segIDs, id)
		snap.stats[id] = Stats{
			MaxDoc:    len(seg.keys),
			NumDocs:   seg.numDocs,
			TermCount: len(seg.postings),
		}
		snap.numDocs += seg.numDocs
		ts := make([]string, 0, len(seg.postings))
		posts := make(map[string][]Posting, len(seg.postings))
		for term, list := range seg.postings {
			ts = append(ts, term)
			cp := make([]Posting, len(list))
			for i, p := range list {
				cp[i] = Posting{Doc: p.Doc, TF: p.TF, Positions: append([]int(nil), p.Positions...)}
			}
			posts[term] = cp
		}
		sort.Strings(ts)
		snap.terms[id] = ts
		snap.postings[id] = posts
	}
	sort.Ints(snap.segIDs)
	return snap
}

func snapshotsEqual(a, b snapshot) (bool, string) {
	if !reflect.DeepEqual(a.segIDs, b.segIDs) {
		return false, "segIDs differ"
	}
	if !reflect.DeepEqual(a.stats, b.stats) {
		return false, "stats differ"
	}
	if !reflect.DeepEqual(a.terms, b.terms) {
		return false, "term sets differ"
	}
	if !reflect.DeepEqual(a.postings, b.postings) {
		return false, "postings differ"
	}
	if a.numDocs != b.liveKeys || b.numDocs != b.liveKeys {
		return false, "numDocs/live-key invariant violated"
	}
	return true, ""
}

func TestSkeleton(t *testing.T) {
	o := NewOracle()
	if o.nextSeg != 1 {
		t.Fatal("oracle skeleton")
	}
	_ = snapshot{}
	_ = rand.Intn
	_ = sort.Strings
	_ = reflect.DeepEqual
}
