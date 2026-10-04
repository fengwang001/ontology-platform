// Package docstore 按分片存取文档，并在分裂/收缩时按记住的 routing 重分布。
package docstore

import (
	"bytes"
	"sync"

	"ontology/shardmap"
	"ontology/slot"
)

// 重新导出 shardmap 的哨兵错误，调用方用 errors.Is 区分。
var (
	ErrInvalidArgument = shardmap.ErrInvalidArgument
	ErrNotFound        = shardmap.ErrNotFound
	ErrReadOnly        = shardmap.ErrReadOnly
	ErrMissingRouting  = shardmap.ErrMissingRouting
	ErrDocumentMissing = shardmap.ErrDocumentMissing
	ErrIDConflict      = shardmap.ErrIDConflict
)

type record struct {
	routing []byte // 实际参与定位的 routing（含 id 充当情形）
	body    []byte
}

type indexData struct {
	mu     sync.Mutex
	shards []map[string]record // 长度随 N 变化；分片内 id 唯一
}

// Touches 记录最近一次 Get 的触碰规模，证明其与总文档量无关。
type Touches struct {
	ShardsTouched int // 触碰的分片数（恒为 1）
	RecordsSeen   int // 检查的文档记录数（0 或 1）
}

// Store 是文档存储的全局句柄。
type Store struct {
	mu      sync.Mutex
	indexes map[string]*indexData

	touchMu        sync.Mutex
	lastGetTouches Touches
}

// New 创建存储并把自己注册为 shardmap 的重分布执行器。
func New() *Store {
	s := &Store{indexes: make(map[string]*indexData)}
	shardmap.RegisterResharder(s)
	return s
}

func (s *Store) data(name string, n int) *indexData {
	s.mu.Lock()
	d, ok := s.indexes[name]
	if !ok {
		d = &indexData{}
		s.indexes[name] = d
	}
	s.mu.Unlock()
	d.mu.Lock()
	for len(d.shards) < n {
		d.shards = append(d.shards, map[string]record{})
	}
	return d
}

func validBytes(b []byte) bool {
	return len(b) >= 1 && len(b) <= 512
}

// Put 在定位到的分片内按 id 唯一写入；同分片同 id 覆盖。
func (s *Store) Put(index string, id, routing, body []byte) error {
	if !validBytes(id) || len(routing) > 512 {
		return ErrInvalidArgument
	}
	st, err := shardmap.Snapshot(index)
	if err != nil {
		return err
	}
	if st.Block {
		return ErrReadOnly
	}
	if st.P > 1 && len(routing) == 0 {
		return ErrMissingRouting
	}
	effective := slot.EffectiveRouting(id, routing)

	for {
		p := st.Params()
		shard := p.Shard(id, routing)
		d := s.data(index, st.N)
		if len(d.shards) != st.N {
			// 持锁期间发生了重分布：释放后用新快照重试，保证串行化。
			d.mu.Unlock()
			st, err = shardmap.Snapshot(index)
			if err != nil {
				return err
			}
			if st.Block {
				return ErrReadOnly
			}
			continue
		}
		stored := make([]byte, len(effective))
		copy(stored, effective)
		bodyCopy := make([]byte, len(body))
		copy(bodyCopy, body)
		d.shards[shard][string(id)] = record{routing: stored, body: bodyCopy}
		d.mu.Unlock()
		return nil
	}
}

// Get 只在定位到的单个分片内查找；不去别的分片。
func (s *Store) Get(index string, id, routing []byte) ([]byte, error) {
	if !validBytes(id) || len(routing) > 512 {
		return nil, ErrInvalidArgument
	}
	st, err := shardmap.Snapshot(index)
	if err != nil {
		return nil, err
	}
	if st.P > 1 && len(routing) == 0 {
		return nil, ErrMissingRouting
	}
	for {
		p := st.Params()
		shard := p.Shard(id, routing)
		d := s.data(index, st.N)
		if len(d.shards) != st.N {
			d.mu.Unlock()
			st, err = shardmap.Snapshot(index)
			if err != nil {
				return nil, err
			}
			continue
		}
		rec, ok := d.shards[shard][string(id)]
		d.mu.Unlock()
		s.noteGetTouches(1, boolToInt(ok))
		if !ok {
			return nil, ErrDocumentMissing
		}
		out := make([]byte, len(rec.body))
		copy(out, rec.body)
		return out, nil
	}
}

// Delete 只在定位到的单个分片内删除；不存在报文档不存在。
func (s *Store) Delete(index string, id, routing []byte) error {
	if !validBytes(id) || len(routing) > 512 {
		return ErrInvalidArgument
	}
	st, err := shardmap.Snapshot(index)
	if err != nil {
		return err
	}
	if st.Block {
		return ErrReadOnly
	}
	if st.P > 1 && len(routing) == 0 {
		return ErrMissingRouting
	}
	for {
		p := st.Params()
		shard := p.Shard(id, routing)
		d := s.data(index, st.N)
		if len(d.shards) != st.N {
			d.mu.Unlock()
			st, err = shardmap.Snapshot(index)
			if err != nil {
				return err
			}
			if st.Block {
				return ErrReadOnly
			}
			continue
		}
		key := string(id)
		if _, ok := d.shards[shard][key]; !ok {
			d.mu.Unlock()
			return ErrDocumentMissing
		}
		delete(d.shards[shard], key)
		d.mu.Unlock()
		return nil
	}
}

// Count 返回各分片（按当前 N）的文档数。
func (s *Store) Count(index string) ([]int, error) {
	st, err := shardmap.Snapshot(index)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	d, ok := s.indexes[index]
	s.mu.Unlock()
	if !ok {
		return make([]int, st.N), nil
	}
	d.mu.Lock()
	counts := make([]int, st.N)
	for i := 0; i < st.N && i < len(d.shards); i++ {
		counts[i] = len(d.shards[i])
	}
	d.mu.Unlock()
	return counts, nil
}

// Reshard 在 shardmap 已持写锁（阻断一切快照与其它重分布）时执行。
// 先在临时表上重分布；收缩撞 id 时返回字节序最小的冲突 id，整体不提交。
func (s *Store) Reshard(indexName string, oldN, newN, r, p int, h slot.HashFunc) ([]byte, error) {
	s.mu.Lock()
	d, ok := s.indexes[indexName]
	if !ok {
		s.mu.Unlock()
		return nil, nil
	}
	s.mu.Unlock()

	d.mu.Lock()
	defer d.mu.Unlock()

	next := make([]map[string]record, newN)
	for i := range next {
		next[i] = map[string]record{}
	}
	params := slot.Params{N: newN, R: r, P: p, H: h}
	var minConflict []byte
	noteConflict := func(id []byte) {
		if minConflict == nil || bytes.Compare(id, minConflict) < 0 {
			minConflict = append(minConflict[:0], id...)
		}
	}
	for _, shard := range d.shards {
		for id, rec := range shard {
			idb := []byte(id)
			target := params.Shard(idb, rec.routing)
			key := string(idb)
			if _, exists := next[target][key]; exists {
				noteConflict(idb)
				continue
			}
			next[target][key] = rec
		}
	}
	if minConflict != nil {
		return minConflict, nil
	}
	d.shards = next
	return nil, nil
}

// LastGetTouches 返回最近一次 Get 触碰的分片数与记录检查数。
func (s *Store) LastGetTouches() Touches {
	s.touchMu.Lock()
	defer s.touchMu.Unlock()
	return s.lastGetTouches
}

func (s *Store) noteGetTouches(shards, records int) {
	s.touchMu.Lock()
	s.lastGetTouches = Touches{ShardsTouched: shards, RecordsSeen: records}
	s.touchMu.Unlock()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
