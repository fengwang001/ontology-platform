package boundedhash

import (
	"errors"
	"sort"
)

type naivePoint struct {
	pos    uint64
	nodeID uint64
}

type naiveKey struct {
	key    string
	seq    int
	pos    uint64
	nodeID uint64
}

type naiveNode struct {
	id     uint64
	points []uint64
	keys   map[string]naiveKey
}

type naiveRing struct {
	cnum      int
	cden      int
	nodes     map[uint64]*naiveNode
	points    []naivePoint
	keys      map[string]naiveKey
	totalKeys int
	nextSeq   int
}

func newNaive(cnum, cden int) *naiveRing {
	return &naiveRing{
		cnum:    cnum,
		cden:    cden,
		nodes:   make(map[uint64]*naiveNode),
		keys:    make(map[string]naiveKey),
		nextSeq: 1,
	}
}

func naiveCapacity(cnum, cden, keyCount, nodeCount int) int {
	return (cnum*keyCount + cden*nodeCount - 1) / (cden * nodeCount)
}

func (n *naiveRing) sortedNodeIDs() []uint64 {
	ids := make([]uint64, 0, len(n.nodes))
	for id := range n.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (n *naiveRing) choose(pos uint64, capacity int) uint64 {
	start := sort.Search(len(n.points), func(i int) bool { return n.points[i].pos >= pos })
	for offset := 0; offset < len(n.points); offset++ {
		point := n.points[(start+offset)%len(n.points)]
		if len(n.nodes[point.nodeID].keys) < capacity {
			return point.nodeID
		}
	}
	panic("naive ring: no node has capacity")
}

func (n *naiveRing) addNode(id uint64, points []uint64) error {
	if id < 1 || len(points) < 1 || len(points) > 64 {
		return ErrInvalidArgument
	}
	copied := append([]uint64(nil), points...)
	sort.Slice(copied, func(i, j int) bool { return copied[i] < copied[j] })
	for i := 1; i < len(copied); i++ {
		if copied[i] == copied[i-1] {
			return ErrInvalidArgument
		}
	}
	if _, ok := n.nodes[id]; ok {
		return ErrNodeExists
	}
	for _, pos := range copied {
		for _, point := range n.points {
			if point.pos == pos {
				return ErrPointConflict
			}
		}
	}

	n.nodes[id] = &naiveNode{id: id, points: copied, keys: make(map[string]naiveKey)}
	for _, pos := range copied {
		n.points = append(n.points, naivePoint{pos: pos, nodeID: id})
	}
	sort.Slice(n.points, func(i, j int) bool { return n.points[i].pos < n.points[j].pos })
	return nil
}

func (n *naiveRing) put(key string, pos uint64) error {
	if key == "" {
		return ErrInvalidArgument
	}
	if len(n.nodes) == 0 {
		return ErrNoNode
	}
	if _, ok := n.keys[key]; ok {
		return ErrKeyExists
	}
	capacity := naiveCapacity(n.cnum, n.cden, n.totalKeys+1, len(n.nodes))
	nodeID := n.choose(pos, capacity)
	record := naiveKey{key: key, seq: n.nextSeq, pos: pos, nodeID: nodeID}
	n.keys[key] = record
	n.nodes[nodeID].keys[key] = record
	n.nextSeq++
	n.totalKeys++
	return nil
}

func (n *naiveRing) delete(key string) error {
	if key == "" {
		return ErrInvalidArgument
	}
	record, ok := n.keys[key]
	if !ok {
		return ErrKeyNotFound
	}
	delete(n.keys, key)
	delete(n.nodes[record.nodeID].keys, key)
	n.totalKeys--
	return nil
}

func (n *naiveRing) lookup(key string) (uint64, error) {
	if key == "" {
		return 0, ErrInvalidArgument
	}
	record, ok := n.keys[key]
	if !ok {
		return 0, ErrKeyNotFound
	}
	return record.nodeID, nil
}

func (n *naiveRing) removeNode(id uint64) error {
	node := n.nodes[id]
	if node == nil {
		return ErrNodeNotFound
	}
	if len(n.nodes) == 1 && len(node.keys) > 0 {
		return ErrCannotClearLastNode
	}

	records := make([]naiveKey, 0, len(node.keys))
	for _, record := range node.keys {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].seq < records[j].seq })

	delete(n.nodes, id)
	remaining := n.points[:0]
	for _, point := range n.points {
		if point.nodeID != id {
			remaining = append(remaining, point)
		}
	}
	n.points = remaining
	n.totalKeys -= len(records)

	for _, record := range records {
		capacity := naiveCapacity(n.cnum, n.cden, n.totalKeys+1, len(n.nodes))
		targetID := n.choose(record.pos, capacity)
		updated := naiveKey{key: record.key, seq: record.seq, pos: record.pos, nodeID: targetID}
		n.keys[record.key] = updated
		n.nodes[targetID].keys[record.key] = updated
		n.totalKeys++
	}
	return nil
}

func (n *naiveRing) rebalance(limit int) ([]Migration, int, error) {
	if limit < 0 || limit > 1_000_000_000 {
		return nil, 0, ErrInvalidArgument
	}
	if len(n.nodes) == 0 {
		return nil, 0, ErrNoNode
	}

	capacity := naiveCapacity(n.cnum, n.cden, n.totalKeys, len(n.nodes))
	migrations := make([]Migration, 0)
	for _, id := range n.sortedNodeIDs() {
		node := n.nodes[id]
		for len(node.keys) > capacity && len(migrations) < limit {
			key := ""
			bestSeq := -1
			for candidate, record := range node.keys {
				if record.seq > bestSeq {
					bestSeq = record.seq
					key = candidate
				}
			}
			record := node.keys[key]
			delete(node.keys, key)
			targetID := n.choose(record.pos, capacity)
			updated := naiveKey{key: key, seq: record.seq, pos: record.pos, nodeID: targetID}
			n.keys[key] = updated
			n.nodes[targetID].keys[key] = updated
			migrations = append(migrations, Migration{Key: key, FromNode: id, ToNode: targetID})
		}
		if len(migrations) == limit {
			break
		}
	}

	excess := 0
	for _, node := range n.nodes {
		if len(node.keys) > capacity {
			excess += len(node.keys) - capacity
		}
	}
	return migrations, excess, nil
}

func errorName(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func sameError(actual, expected error) bool {
	if actual == nil || expected == nil {
		return actual == expected
	}
	return errors.Is(actual, expected)
}
