package alloc

import (
	"fmt"
	"sort"
	"strings"

	"ontology/decider"
	"ontology/node"
)

// Independent, deliberately straightforward reimplementation of the spec.
// It shares no logic with the production allocator and serves as the oracle
// for the random-sequence differential test.

type nCopy struct {
	index   string
	shard   int
	replica int
	primary bool
	size    int64
	host    string
}

type nNode struct {
	id, zone string
	total    int64
	other    int64
	exclude  bool
}

type nIndex struct {
	s, r int
	size int64
}

type naive struct {
	l, h   int
	nodes  map[string]*nNode
	idxs   map[string]*nIndex
	copies []*nCopy
	log    []string
}

func newNaive(l, h int) *naive {
	return &naive{l: l, h: h, nodes: map[string]*nNode{}, idxs: map[string]*nIndex{}}
}

func (m *naive) note(format string, a ...any) {
	m.log = append(m.log, fmt.Sprintf(format, a...))
}

func (m *naive) addNode(id, zone string, total int64) error {
	if len(id) < 1 || len(id) > 64 || len(zone) < 1 || len(zone) > 64 || total < 1 || total > 1e12 {
		return node.ErrInvalidArg
	}
	if _, ok := m.nodes[id]; ok {
		return node.ErrAlreadyExists
	}
	m.nodes[id] = &nNode{id: id, zone: zone, total: total}
	return nil
}

func (m *naive) setOther(id string, v int64) error {
	if v < 0 || v > 1e12 {
		return node.ErrInvalidArg
	}
	n, ok := m.nodes[id]
	if !ok {
		return node.ErrNotFound
	}
	n.other = v
	return nil
}

func (m *naive) setExclude(id string, on bool) error {
	n, ok := m.nodes[id]
	if !ok {
		return node.ErrNotFound
	}
	n.exclude = on
	return nil
}

func (m *naive) createIndex(name string, s, r int, size int64) error {
	if len(name) < 1 || len(name) > 64 || s < 1 || s > 64 || r < 0 || r > 5 || size < 1 || size > 1e12 {
		return node.ErrInvalidArg
	}
	if _, ok := m.idxs[name]; ok {
		return node.ErrAlreadyExists
	}
	m.idxs[name] = &nIndex{s: s, r: r, size: size}
	for sh := 0; sh < s; sh++ {
		m.copies = append(m.copies, &nCopy{index: name, shard: sh, primary: true, size: size})
		for rep := 0; rep < r; rep++ {
			m.copies = append(m.copies, &nCopy{index: name, shard: sh, replica: rep, size: size})
		}
	}
	return nil
}

func (m *naive) sortedNodeIDs() []string {
	ids := make([]string, 0, len(m.nodes))
	for id := range m.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *naive) find(index string, sh, rep int, primary bool) *nCopy {
	for _, cp := range m.copies {
		if cp.index == index && cp.shard == sh && cp.primary == primary &&
			(primary || cp.replica == rep) {
			return cp
		}
	}
	return nil
}

func (m *naive) zoneCount() int {
	zones := map[string]struct{}{}
	for _, n := range m.nodes {
		zones[n.zone] = struct{}{}
	}
	return len(zones)
}

func (m *naive) used(id string) int64 {
	u := m.nodes[id].other
	for _, cp := range m.copies {
		if cp.host == id {
			u += cp.size
		}
	}
	return u
}

func (m *naive) copyCount(id string) int {
	c := 0
	for _, cp := range m.copies {
		if cp.host == id {
			c++
		}
	}
	return c
}

func (m *naive) zoneCopies(zone, index string, sh int) int {
	c := 0
	for _, cp := range m.copies {
		if cp.host == "" || cp.index != index || cp.shard != sh {
			continue
		}
		if m.nodes[cp.host].zone == zone {
			c++
		}
	}
	return c
}

// decide is an independent implementation of D1-D4.
func (m *naive) decide(id string, cp *nCopy, moving bool) decider.Reason {
	n := m.nodes[id]
	if n.exclude {
		return decider.D1Excluded
	}
	for _, other := range m.copies {
		if other.host == id && other.index == cp.index && other.shard == cp.shard {
			return decider.D2SameShard
		}
	}
	ix := m.idxs[cp.index]
	z := m.zoneCount()
	perZone := (1 + ix.r + z - 1) / z
	if m.zoneCopies(n.zone, cp.index, cp.shard)+1 > perZone {
		return decider.D3Awareness
	}
	pct := m.l
	if cp.primary && !moving {
		pct = m.h
	}
	if (m.used(id)+cp.size)*100 > int64(pct)*n.total {
		return decider.D4Disk
	}
	return decider.OK
}

func (m *naive) choose(cp *nCopy, source string, moving bool) (string, []decider.Reason) {
	reasons := []decider.Reason{}
	best, bestCount := "", 0
	for _, id := range m.sortedNodeIDs() {
		if id == source {
			reasons = append(reasons, 0)
			continue
		}
		r := m.decide(id, cp, moving)
		reasons = append(reasons, r)
		if r != decider.OK {
			continue
		}
		cnt := m.copyCount(id)
		if best == "" || cnt < bestCount || (cnt == bestCount && id < best) {
			best, bestCount = id, cnt
		}
	}
	return best, reasons
}

func (m *naive) reasonLine(chosen string, reasons []decider.Reason) string {
	ids := m.sortedNodeIDs()
	parts := make([]string, 0, len(ids))
	for i, id := range ids {
		tag := reasons[i].String()
		if id == chosen {
			tag = "PICK"
		}
		parts = append(parts, id+":"+tag)
	}
	return strings.Join(parts, " ")
}

func (m *naive) placement() map[string]string {
	out := map[string]string{}
	for _, cp := range m.copies {
		if cp.host != "" {
			out[placementKey(cp.index, cp.shard, cp.primary, cp.replica)] = cp.host
		}
	}
	return out
}

func placementKey(index string, sh int, primary bool, rep int) string {
	return fmt.Sprintf("%s/%d/%v/%d", index, sh, primary, rep)
}

func (m *naive) reroute() Result {
	res := Result{Assigned: []Movement{}, Moved: []Movement{}}
	indexNames := make([]string, 0, len(m.idxs))
	for name := range m.idxs {
		indexNames = append(indexNames, name)
	}
	sort.Strings(indexNames)

	for _, name := range indexNames {
		ix := m.idxs[name]
		for sh := 0; sh < ix.s; sh++ {
			p := m.find(name, sh, 0, true)
			if p.host == "" {
				target, reasons := m.choose(p, "", false)
				m.note("phase1 %s/%d P %s", name, sh, m.reasonLine(target, reasons))
				if target == "" {
					continue
				}
				p.host = target
				res.Assigned = append(res.Assigned, Movement{Index: name, Shard: sh, Primary: true, To: target})
			}
			for rep := 0; rep < ix.r; rep++ {
				rp := m.find(name, sh, rep, false)
				if rp.host != "" {
					continue
				}
				target, reasons := m.choose(rp, "", false)
				m.note("phase1 %s/%d R%d %s", name, sh, rep, m.reasonLine(target, reasons))
				if target == "" {
					continue
				}
				rp.host = target
				res.Assigned = append(res.Assigned, Movement{Index: name, Shard: sh, Primary: false, To: target})
			}
		}
	}

	for _, id := range m.sortedNodeIDs() {
		n := m.nodes[id]
		if !n.exclude && m.used(id)*100 <= int64(m.h)*n.total {
			continue
		}
		onNode := []*nCopy{}
		for _, cp := range m.copies {
			if cp.host == id {
				onNode = append(onNode, cp)
			}
		}
		sort.Slice(onNode, func(i, j int) bool {
			a, b := onNode[i], onNode[j]
			if a.size != b.size {
				return a.size > b.size
			}
			if a.index != b.index {
				return a.index < b.index
			}
			if a.shard != b.shard {
				return a.shard < b.shard
			}
			if a.primary != b.primary {
				return a.primary
			}
			return a.replica < b.replica
		})
		for _, cp := range onNode {
			if cp.host != id {
				continue
			}
			if !n.exclude && m.used(id)*100 <= int64(m.h)*n.total {
				break
			}
			cp.host = ""
			target, reasons := m.choose(cp, id, true)
			m.note("phase2 %s/%d primary=%v off %s %s",
				cp.index, cp.shard, cp.primary, id, m.reasonLine(target, reasons))
			if target == "" {
				cp.host = id
				continue
			}
			cp.host = target
			res.Moved = append(res.Moved, Movement{
				Index: cp.index, Shard: cp.shard, Primary: cp.primary, From: id, To: target,
			})
		}
	}
	return res
}

func (m *naive) explain(index string, sh int, primary bool) ([]Verdict, error) {
	ix, ok := m.idxs[index]
	if !ok {
		return nil, node.ErrNotFound
	}
	if sh < 0 || sh >= ix.s {
		return nil, node.ErrShardMissing
	}
	cp := m.find(index, sh, 0, primary)
	if cp == nil { // e.g. asking about a replica of an index with r=0
		cp = &nCopy{index: index, shard: sh, primary: primary, size: ix.size}
	}
	primaryUp := !primary && m.find(index, sh, 0, true).host == ""
	out := []Verdict{}
	for _, id := range m.sortedNodeIDs() {
		out = append(out, Verdict{NodeID: id, Reason: m.decide(id, cp, false), PrimaryUp: primaryUp})
	}
	return out, nil
}
