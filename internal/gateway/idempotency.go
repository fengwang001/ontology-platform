package gateway

import (
	"hash/fnv"
	"sync"
)

// idemRecord 幂等记录。键为 (提交者, 请求编号)。
type idemRecord struct {
	shard     *idemShard  // 所属分片（创建时绑定）
	replaced  *idemRecord // 占位时被替换的过期记录（abort 时恢复）
	content   string      // 请求内容的规范化表示
	acceptAt  int64       // 受理时刻
	resolved  bool        // 原提交是否已完结（受理或被拒）
	rejected  bool        // 原提交是否最终被拒绝（记录随即删除）
	vehicleID string
	commandID string
	done      chan struct{} // resolved 后关闭
}

type idemShard struct {
	mu sync.Mutex
	m  map[string]*idemRecord
}

// idemStore 幂等存储，按键哈希分片以避免跨车辆阻塞。
// 记录自受理起保留固定时长，超过后同一键视为新请求。
type idemStore struct {
	shards    [64]idemShard
	retention int64
}

func newIdemStore(retention int64) *idemStore {
	s := &idemStore{retention: retention}
	for i := range s.shards {
		s.shards[i].m = make(map[string]*idemRecord)
	}
	return s
}

func (s *idemStore) shard(key string) *idemShard {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return &s.shards[h.Sum64()%uint64(len(s.shards))]
}

// idemDup 表示命中同键同内容的有效记录。
type idemDup struct {
	vehicleID string
	commandID string
}

// acquire 检查并占位幂等键。
//   - 命中有效记录且内容不同：返回幂等冲突错误；
//   - 命中有效记录且内容相同：返回 dup（原指令结果）；
//   - 否则占位并返回新记录，调用方后续必须 commit 或 abort。
//
// 原提交仍在进行时到达的同键请求会等待其完结：若原提交被受理则按重复
// 处理，若原提交被拒绝则重新占位（被拒绝的提交不占用幂等键）。
func (s *idemStore) acquire(key, content string, t int64) (rec *idemRecord, dup *idemDup, err error) {
	sh := s.shard(key)
	for {
		sh.mu.Lock()
		r := sh.m[key]
		switch {
		case r == nil || t > r.acceptAt+s.retention:
			// 不存在或已过期：占位为新请求。
			nr := &idemRecord{shard: sh, replaced: r, content: content, acceptAt: t, done: make(chan struct{})}
			sh.m[key] = nr
			sh.mu.Unlock()
			return nr, nil, nil
		case r.content != content:
			sh.mu.Unlock()
			return nil, nil, rejectf(RejectIdempotencyConflict, "幂等键内容冲突")
		case !r.resolved:
			// 原提交进行中：等待其完结后重试。
			done := r.done
			sh.mu.Unlock()
			<-done
		case r.rejected:
			// 原提交被拒绝，记录已删除：重新占位。
			continue
		default:
			d := &idemDup{vehicleID: r.vehicleID, commandID: r.commandID}
			sh.mu.Unlock()
			return nil, d, nil
		}
	}
}

// commit 标记占位记录对应的原提交已被受理。
func (s *idemStore) commit(rec *idemRecord, vehicleID, commandID string) {
	sh := s.shardOf(rec)
	sh.mu.Lock()
	rec.vehicleID = vehicleID
	rec.commandID = commandID
	rec.resolved = true
	close(rec.done)
	sh.mu.Unlock()
}

// abort 撤销占位记录（原提交被拒绝，不占用幂等键）。
func (s *idemStore) abort(key string, rec *idemRecord) {
	sh := s.shard(key)
	sh.mu.Lock()
	if cur := sh.m[key]; cur == rec {
		if rec.replaced != nil {
			sh.m[key] = rec.replaced // 恢复被替换的过期记录
		} else {
			delete(sh.m, key)
		}
	}
	rec.rejected = true
	rec.resolved = true
	close(rec.done)
	sh.mu.Unlock()
}

// shardOf 返回记录创建时绑定的分片。
func (s *idemStore) shardOf(rec *idemRecord) *idemShard {
	return rec.shard
}

func (s *idemStore) count() int {
	n := 0
	for i := range s.shards {
		s.shards[i].mu.Lock()
		n += len(s.shards[i].m)
		s.shards[i].mu.Unlock()
	}
	return n
}
