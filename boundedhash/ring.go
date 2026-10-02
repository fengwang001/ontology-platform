package boundedhash

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidConfig       = errors.New("boundedhash: invalid load coefficient")
	ErrInvalidArgument     = errors.New("boundedhash: invalid argument")
	ErrNoNode              = errors.New("boundedhash: no node")
	ErrKeyExists           = errors.New("boundedhash: key already exists")
	ErrNodeExists          = errors.New("boundedhash: node already exists")
	ErrPointConflict       = errors.New("boundedhash: ring point conflict")
	ErrNodeNotFound        = errors.New("boundedhash: node not found")
	ErrKeyNotFound         = errors.New("boundedhash: key not found")
	ErrCannotClearLastNode = errors.New("boundedhash: cannot remove last node while it owns keys")
)

type Migration struct {
	Key      string
	FromNode uint64
	ToNode   uint64
}

type ringPoint struct {
	pos    uint64
	nodeID uint64
}

type keyRecord struct {
	key    string
	seq    int
	nodeID uint64
	pos    uint64
}

type nodeState struct {
	points []uint64
	keys   map[string]keyRecord
	seq    []string
}

type Ring struct {
	mu         sync.RWMutex
	cnum       int
	cden       int
	nodes      map[uint64]*nodeState
	nodeIDs    []uint64
	points     []ringPoint
	pointOwner map[uint64]uint64
	keys       map[string]keyRecord
	totalKeys  int
	nextSeq    int

	binarySearchComparisons int64
}

func (r *Ring) binarySearchComparisonCountLocked() int64 {
	return r.binarySearchComparisons
}

func New(cnum, cden int) (*Ring, error) {
	if cden < 1 || cnum < cden || cnum > 1_000_000 {
		return nil, ErrInvalidConfig
	}
	return &Ring{
		cnum:       cnum,
		cden:       cden,
		nodes:      make(map[uint64]*nodeState),
		pointOwner: make(map[uint64]uint64),
		keys:       make(map[string]keyRecord),
		nextSeq:    1,
	}, nil
}

func (r *Ring) AddNode(id uint64, points []uint64) error {
	if id < 1 || len(points) < 1 || len(points) > 64 {
		return ErrInvalidArgument
	}

	seen := make(map[uint64]struct{}, len(points))
	copied := make([]uint64, len(points))
	copy(copied, points)
	sort.Slice(copied, func(i, j int) bool { return copied[i] < copied[j] })
	for _, pos := range copied {
		if _, ok := seen[pos]; ok {
			return ErrInvalidArgument
		}
		seen[pos] = struct{}{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.nodes[id]; ok {
		return ErrNodeExists
	}
	for _, pos := range copied {
		if _, ok := r.pointOwner[pos]; ok {
			return ErrPointConflict
		}
	}

	node := &nodeState{
		points: copied,
		keys:   make(map[string]keyRecord),
	}
	r.nodes[id] = node
	insertAt := sort.Search(len(r.nodeIDs), func(i int) bool { return r.nodeIDs[i] >= id })
	r.nodeIDs = append(r.nodeIDs, 0)
	copy(r.nodeIDs[insertAt+1:], r.nodeIDs[insertAt:])
	r.nodeIDs[insertAt] = id

	merged := make([]ringPoint, 0, len(r.points)+len(copied))
	oldIndex, newIndex := 0, 0
	for oldIndex < len(r.points) || newIndex < len(copied) {
		if newIndex == len(copied) || (oldIndex < len(r.points) && r.points[oldIndex].pos < copied[newIndex]) {
			merged = append(merged, r.points[oldIndex])
			oldIndex++
		} else {
			merged = append(merged, ringPoint{pos: copied[newIndex], nodeID: id})
			newIndex++
		}
	}
	r.points = merged
	for _, pos := range copied {
		r.pointOwner[pos] = id
	}
	return nil
}

func (r *Ring) Put(key string, pos uint64) error {
	if key == "" {
		return ErrInvalidArgument
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.nodes) == 0 {
		return ErrNoNode
	}
	if _, ok := r.keys[key]; ok {
		return ErrKeyExists
	}

	nodeID := r.chooseNodeLocked(pos, r.putCapacity())
	r.assignKeyLocked(key, pos, nodeID, r.nextSeq)
	r.nextSeq++
	r.totalKeys++
	return nil
}

func (r *Ring) Delete(key string) error {
	if key == "" {
		return ErrInvalidArgument
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	record, ok := r.keys[key]
	if !ok {
		return ErrKeyNotFound
	}
	r.detachKeyLocked(key, record.nodeID)
	delete(r.keys, key)
	r.totalKeys--
	return nil
}

func (r *Ring) Lookup(key string) (uint64, error) {
	if key == "" {
		return 0, ErrInvalidArgument
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	record, ok := r.keys[key]
	if !ok {
		return 0, ErrKeyNotFound
	}
	return record.nodeID, nil
}

func (r *Ring) Loads() map[uint64]int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[uint64]int, len(r.nodes))
	for id, node := range r.nodes {
		result[id] = len(node.keys)
	}
	return result
}

func (r *Ring) RemoveNode(id uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	node := r.nodes[id]
	if node == nil {
		return ErrNodeNotFound
	}
	if len(r.nodes) == 1 && len(node.keys) > 0 {
		return ErrCannotClearLastNode
	}

	records := make([]keyRecord, 0, len(node.keys))
	for _, key := range node.seq {
		records = append(records, node.keys[key])
	}
	sort.Slice(records, func(i, j int) bool { return records[i].seq < records[j].seq })

	delete(r.nodes, id)
	deleteIndex := sort.Search(len(r.nodeIDs), func(i int) bool { return r.nodeIDs[i] >= id })
	r.nodeIDs = append(r.nodeIDs[:deleteIndex], r.nodeIDs[deleteIndex+1:]...)

	remainingPoints := r.points[:0]
	for _, point := range r.points {
		if point.nodeID != id {
			remainingPoints = append(remainingPoints, point)
		} else {
			delete(r.pointOwner, point.pos)
		}
	}
	r.points = remainingPoints

	r.totalKeys -= len(records)
	for _, record := range records {
		capacity := capacityFor(r.cnum, r.cden, r.totalKeys+1, len(r.nodes))
		targetID := r.chooseNodeLocked(record.pos, capacity)
		r.assignKeyLocked(record.key, record.pos, targetID, record.seq)
		r.totalKeys++
	}
	return nil
}

func (r *Ring) Rebalance(limit int) ([]Migration, int, error) {
	if limit < 0 || limit > 1_000_000_000 {
		return nil, 0, ErrInvalidArgument
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.nodes) == 0 {
		return nil, 0, ErrNoNode
	}

	targetCapacity := capacityFor(r.cnum, r.cden, r.totalKeys, len(r.nodes))
	migrations := make([]Migration, 0)

	for _, nodeID := range r.nodeIDs {
		node := r.nodes[nodeID]
		for len(node.keys) > targetCapacity && len(migrations) < limit {
			key := node.seq[0]
			record := node.keys[key]
			r.detachKeyLocked(key, nodeID)
			targetID := r.chooseNodeLocked(record.pos, targetCapacity)
			r.assignKeyLocked(key, record.pos, targetID, record.seq)
			migrations = append(migrations, Migration{
				Key:      key,
				FromNode: nodeID,
				ToNode:   targetID,
			})
		}
		if len(migrations) == limit {
			break
		}
	}

	return migrations, r.excessLocked(targetCapacity), nil
}

func (r *Ring) putCapacity() int {
	return capacityFor(r.cnum, r.cden, r.totalKeys+1, len(r.nodes))
}

func capacityFor(cnum, cden, keyCount, nodeCount int) int {
	numerator := int64(cnum) * int64(keyCount)
	denominator := int64(cden) * int64(nodeCount)
	return int((numerator + denominator - 1) / denominator)
}

func (r *Ring) chooseNodeLocked(pos uint64, capacity int) uint64 {
	start := r.lowerBoundLocked(pos)
	pointCount := len(r.points)
	for offset := 0; offset < pointCount; offset++ {
		point := r.points[(start+offset)%pointCount]
		if len(r.nodes[point.nodeID].keys) < capacity {
			return point.nodeID
		}
	}
	panic("boundedhash: no node has capacity")
}

func (r *Ring) lowerBoundLocked(pos uint64) int {
	lo, hi := 0, len(r.points)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		r.binarySearchComparisons++
		if r.points[mid].pos < pos {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func (r *Ring) assignKeyLocked(key string, pos, nodeID uint64, seq int) {
	record := keyRecord{key: key, seq: seq, nodeID: nodeID, pos: pos}
	r.keys[key] = record
	node := r.nodes[nodeID]
	node.keys[key] = record
	insertAt := sort.Search(len(node.seq), func(i int) bool {
		return node.keys[node.seq[i]].seq <= seq
	})
	node.seq = append(node.seq, "")
	copy(node.seq[insertAt+1:], node.seq[insertAt:])
	node.seq[insertAt] = key
}

func (r *Ring) detachKeyLocked(key string, nodeID uint64) {
	node := r.nodes[nodeID]
	delete(node.keys, key)
	for i, candidate := range node.seq {
		if candidate == key {
			node.seq = append(node.seq[:i], node.seq[i+1:]...)
			break
		}
	}
}

func (r *Ring) excessLocked(capacity int) int {
	excess := 0
	for _, nodeID := range r.nodeIDs {
		load := len(r.nodes[nodeID].keys)
		if load > capacity {
			excess += load - capacity
		}
	}
	return excess
}
