package link

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

func (s *Store) typeByID(id LinkTypeID) *LinkType {
	s.typesMu.RLock()
	defer s.typesMu.RUnlock()
	return s.types[id]
}

func (s *Store) record(op string, req CreateRequest, reason string, result DecisionResult, linkID uint64) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.seq++
	s.decisions = append(s.decisions, DecisionLog{
		Seq: s.seq, Op: op, Request: cloneRequest(req), Reason: reason,
		Result: result, LinkID: linkID,
	})
}

// recordLocked 在链接临界区内调用，使日志 Seq 与登记效果的全序严格一致。
func (s *Store) recordLocked(op string, req CreateRequest, reason string, result DecisionResult, linkID uint64) {
	s.record(op, req, reason, result, linkID)
}

func (s *Store) lockIndices(idxs ...int) []int {
	sort.Ints(idxs)
	uniq := idxs[:0]
	for i, v := range idxs {
		if i == 0 || v != idxs[i-1] {
			uniq = append(uniq, v)
		}
	}
	return uniq
}

func (s *Store) lockAll(idxs []int) {
	for _, i := range idxs {
		s.shards[i].mu.Lock()
	}
}

func (s *Store) unlockAll(idxs []int) {
	for i := len(idxs) - 1; i >= 0; i-- {
		s.shards[idxs[i]].mu.Unlock()
	}
}

// resolveDirection 判定 (srcType,tgtType) 相对链接类型声明的方向。
func resolveDirection(t *LinkType, srcType, tgtType ObjectTypeID,
	srcID, tgtID ObjectInstanceID) (Direction, error) {
	if t == nil {
		return 0, ErrUnknownLinkType
	}
	fwd := srcType == t.SourceType && tgtType == t.TargetType
	bwd := srcType == t.TargetType && tgtType == t.SourceType
	switch {
	case fwd && bwd:
		// 自引用链接类型（两端对象类型相同）：用实例 ID 的全序消歧；
		// 同一实例的自链接固定计入 forward。
		if srcID == tgtID || srcID < tgtID {
			return DirectionForward, nil
		}
		return DirectionBackward, nil
	case fwd:
		return DirectionForward, nil
	case bwd:
		return DirectionBackward, nil
	default:
		return 0, ErrLinkTypeDirectionNotAllowed
	}
}

func makePairKey(typeID LinkTypeID, a, b ObjectInstanceID) pairKey {
	if a <= b {
		return pairKey{typeID: typeID, lo: a, hi: b}
	}
	return pairKey{typeID: typeID, lo: b, hi: a}
}

// canonicalDiscriminator 将区分属性组合规范化为与映射迭代顺序无关的稳定键。
func canonicalDiscriminator(d map[string]string) string {
	if len(d) == 0 {
		return ""
	}
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(0)
		b.WriteString(d[k])
		b.WriteByte(0)
	}
	return b.String()
}

func cloneDiscriminator(d map[string]string) map[string]string {
	if d == nil {
		return nil
	}
	out := make(map[string]string, len(d))
	for k, v := range d {
		out[k] = v
	}
	return out
}

func cloneRequest(req CreateRequest) CreateRequest {
	req.Discriminator = cloneDiscriminator(req.Discriminator)
	return req
}

func opposite(d Direction) Direction {
	if d == DirectionForward {
		return DirectionBackward
	}
	return DirectionForward
}

func forwardCap(t *LinkType, dir Direction) Cardinality {
	if dir == DirectionForward {
		return t.ForwardCap
	}
	return t.BackwardCap
}

func dirMap(ps *pairState, dir Direction) map[string]*Link {
	if dir == DirectionForward {
		return ps.forward
	}
	return ps.backward
}

func dirCount(ps *pairState, dir Direction) int {
	if ps == nil {
		return 0
	}
	return len(dirMap(ps, dir))
}

func lookupLink(ps *pairState, dir Direction, disc string) *Link {
	if ps == nil {
		return nil
	}
	return dirMap(ps, dir)[disc]
}

func releaseDegree(sh *shard, k degreeKey) {
	if sh.degree[k] <= 1 {
		delete(sh.degree, k)
		return
	}
	sh.degree[k]--
}

func pairShard(k pairKey) int {
	h := fnv.New32a()
	fmt.Fprintf(h, "p\x00%s\x00%s\x00%s", k.typeID, k.lo, k.hi)
	return int(h.Sum32() % shardCount)
}

func degreeShard(k degreeKey) int {
	h := fnv.New32a()
	fmt.Fprintf(h, "d\x00%s\x00%s\x00%d", k.typeID, k.instanceID, k.direction)
	return int(h.Sum32() % shardCount)
}
