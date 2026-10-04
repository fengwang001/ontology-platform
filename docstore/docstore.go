// Package docstore 按分片存取文档，并在分裂/收缩时重分布文档。
package docstore

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/shardmap"
	"ontology/slot"
)

var (
	ErrInvalidParam       = shardmap.ErrInvalidParam
	ErrIndexExists        = shardmap.ErrIndexExists
	ErrIndexNotFound      = shardmap.ErrIndexNotFound
	ErrIndexReadOnly      = shardmap.ErrIndexReadOnly
	ErrWriteBlockRequired = shardmap.ErrWriteBlockRequired
	ErrMissingRouting     = shardmap.ErrMissingRouting
	ErrCannotSplit        = shardmap.ErrCannotSplit
	ErrCannotShrink       = shardmap.ErrCannotShrink

	ErrIDConflict       = errors.New("id conflict during shrink")
	ErrDocumentNotFound = errors.New("document not found")
)

// HashFunc 与 slot.HashFunc 相同，便于调用方注入。
type HashFunc = slot.HashFunc

// FNV1a32 为默认哈希。
var FNV1a32 = slot.FNV1a32

type document struct {
	body    []byte
	routing []byte
}

type shard map[string]document

type store struct {
	meta *shardmap.Index

	mu     sync.Mutex
	shards []shard

	touchedShards int
	touchedDocs   int
}

var (
	storesMu sync.Mutex
	stores   = map[string]*store{}
)

// CreateIndex 创建索引。
func CreateIndex(name string, N, R, P int, opts ...shardmap.Option) error {
	storesMu.Lock()
	if _, ok := stores[name]; ok {
		storesMu.Unlock()
		return ErrIndexExists
	}
	meta, err := shardmap.CreateIndex(name, N, R, P, opts...)
	if err != nil {
		storesMu.Unlock()
		return err
	}
	shards := make([]shard, N)
	for i := range shards {
		shards[i] = shard{}
	}
	stores[name] = &store{meta: meta, shards: shards}
	storesMu.Unlock()
	return nil
}

// SetWriteBlock 设置写阻塞。
func SetWriteBlock(index string, on bool) error {
	return shardmap.SetWriteBlock(index, on)
}

// Put 写入或覆盖定位分片内的文档。
func Put(index string, id, routing, body []byte) error {
	if err := validateKey(id); err != nil {
		return err
	}
	if len(routing) > 512 {
		return ErrInvalidParam
	}
	st, snap, err := lookup(index)
	if err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.meta.WriteBlocked() {
		return ErrIndexReadOnly
	}
	if err := validateRouting(routing, snap.P); err != nil {
		return err
	}

	snap = st.meta.Snapshot()
	effective := effectiveRouting(id, routing)
	sh := locate(snap, id, effective)
	bodyCopy := append([]byte(nil), body...)
	st.shards[sh][string(id)] = document{body: bodyCopy, routing: append([]byte(nil), effective...)}
	return nil
}

// Get 只在定位分片内读取文档。
func Get(index string, id, routing []byte) ([]byte, error) {
	if err := validateKey(id); err != nil {
		return nil, err
	}
	if len(routing) > 512 {
		return nil, ErrInvalidParam
	}
	st, snap, err := lookup(index)
	if err != nil {
		return nil, err
	}
	if err := validateRouting(routing, snap.P); err != nil {
		return nil, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	snap = st.meta.Snapshot()
	effective := effectiveRouting(id, routing)
	sh := locate(snap, id, effective)

	st.touchedShards = 1
	doc, ok := st.shards[sh][string(id)]
	if ok {
		st.touchedDocs = 1
		return append([]byte(nil), doc.body...), nil
	}
	st.touchedDocs = 0
	return nil, ErrDocumentNotFound
}

// Delete 只在定位分片内删除文档。
func Delete(index string, id, routing []byte) error {
	if err := validateKey(id); err != nil {
		return err
	}
	if len(routing) > 512 {
		return ErrInvalidParam
	}
	st, snap, err := lookup(index)
	if err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.meta.WriteBlocked() {
		return ErrIndexReadOnly
	}
	if err := validateRouting(routing, snap.P); err != nil {
		return err
	}

	snap = st.meta.Snapshot()
	effective := effectiveRouting(id, routing)
	sh := locate(snap, id, effective)
	if _, ok := st.shards[sh][string(id)]; !ok {
		return ErrDocumentNotFound
	}
	delete(st.shards[sh], string(id))
	return nil
}

// Count 返回各分片文档数。
func Count(index string) ([]int, error) {
	st, _, err := lookup(index)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	counts := make([]int, len(st.shards))
	for i, sh := range st.shards {
		counts[i] = len(sh)
	}
	return counts, nil
}

// SearchShards 返回 routing 的文档可能落入的全部分片（升序去重）。
func SearchShards(index string, routing []byte) ([]int, error) {
	if len(routing) > 512 {
		return nil, ErrInvalidParam
	}
	st, snap, err := lookup(index)
	if err != nil {
		return nil, err
	}
	if len(routing) == 0 {
		return nil, ErrMissingRouting
	}
	st.mu.Lock()
	defer st.mu.Unlock()

	snap = st.meta.Snapshot()
	hr := snap.Hash(routing)
	set := map[int]struct{}{}
	for _, s := range slot.SearchSlots(hr, snap.R, snap.P) {
		set[slot.Shard(s, snap.N, snap.R)] = struct{}{}
	}
	out := make([]int, 0, len(set))
	for sh := range set {
		out = append(out, sh)
	}
	sort.Ints(out)
	return out, nil
}

// Split 在写阻塞状态下把主分片数增至 N2 并重分布。
func Split(index string, N2 int) error {
	if N2 < 1 || N2 > 1024 {
		return ErrInvalidParam
	}
	st, _, err := lookup(index)
	if err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.meta.WriteBlocked() {
		return ErrWriteBlockRequired
	}
	if err := st.meta.ValidateSplit(N2); err != nil {
		return err
	}

	old := st.meta.Snapshot()
	snap := shardmap.Snapshot{N: N2, R: old.R, P: old.P, Hash: old.Hash}
	next := make([]shard, N2)
	for i := range next {
		next[i] = shard{}
	}
	for _, sh := range st.shards {
		for id, doc := range sh {
			target := locate(snap, []byte(id), doc.routing)
			next[target][id] = doc
		}
	}
	st.shards = next
	st.meta.CommitN(N2)
	return nil
}

// Shrink 在写阻塞状态下把主分片数减至 N2 并重分布。
func Shrink(index string, N2 int) error {
	if N2 < 1 || N2 > 1024 {
		return ErrInvalidParam
	}
	st, _, err := lookup(index)
	if err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.meta.WriteBlocked() {
		return ErrWriteBlockRequired
	}
	if err := st.meta.ValidateShrink(N2); err != nil {
		return err
	}

	old := st.meta.Snapshot()
	snap := shardmap.Snapshot{N: N2, R: old.R, P: old.P, Hash: old.Hash}
	next := make([]shard, N2)
	for i := range next {
		next[i] = shard{}
	}
	var minConflict []byte
	for _, sh := range st.shards {
		for id, doc := range sh {
			target := locate(snap, []byte(id), doc.routing)
			if existing, ok := next[target][id]; ok {
				if !bytesEqual(existing.routing, doc.routing) {
					if minConflict == nil || string(id) < string(minConflict) {
						minConflict = append([]byte(nil), id...)
					}
				}
			}
			next[target][id] = doc
		}
	}
	if minConflict != nil {
		return idConflictError(minConflict)
	}
	st.shards = next
	st.meta.CommitN(N2)
	return nil
}

func lookup(index string) (*store, shardmap.Snapshot, error) {
	storesMu.Lock()
	st := stores[index]
	storesMu.Unlock()
	if st == nil {
		return nil, shardmap.Snapshot{}, ErrIndexNotFound
	}
	return st, st.meta.Snapshot(), nil
}

func locate(snap shardmap.Snapshot, id, routing []byte) int {
	hid := snap.Hash(id)
	hr := snap.Hash(routing)
	return slot.Shard(slot.Slot(hid, hr, snap.R, snap.P), snap.N, snap.R)
}

func effectiveRouting(id, routing []byte) []byte {
	if len(routing) > 0 {
		return routing
	}
	return id
}

func validateKey(id []byte) error {
	if len(id) < 1 || len(id) > 512 {
		return ErrInvalidParam
	}
	return nil
}

func validateRouting(routing []byte, P int) error {
	if len(routing) == 0 {
		if P > 1 {
			return ErrMissingRouting
		}
		return nil
	}
	if len(routing) > 512 {
		return ErrInvalidParam
	}
	return nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func idConflictError(id []byte) error {
	return fmt.Errorf("%w: %s", ErrIDConflict, string(id))
}
