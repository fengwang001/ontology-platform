package cardinality

import "sort"

// Unlimited 表示不设基数上限（默认值）。
const Unlimited = int(^uint(0) >> 1)

// store 是引擎的全部内部状态。Engine 通过单一互斥锁保护整个 store，
// 从而为所有变更操作赋予确定的全局全序。
type store struct {
	// objectValidFn 判定对象是否仍有效（未被撤销）；nil 表示恒有效。
	objectValidFn func(objectID string) bool

	// seq 为全局全序计数器，每个变更操作取号后即确定其串行位置。
	seq int64

	links   map[string]*Link
	buckets map[BucketKey]*bucket

	derived map[string]*DerivedState

	// revoked 记录已被撤销的对象。
	revoked map[string]bool

	auditLog []AuditRecord
}

// bucket 是单个（链接类型, 方向, 源对象）基数桶的状态。
type bucket struct {
	key BucketKey

	// baseLimit 为用户通过 SetLimit 设定的基础上限；Unlimited 表示不限。
	baseLimit int

	// members 为桶内全部已登记且未物理删除的链接 ID（含 pending）。
	members map[string]bool

	// retained 为经显式保留处置而豁免基数限制的链接 ID 集合。
	// 它的大小决定了"因保留而提升的有效上限"：
	// 有效容量 = max(baseLimit, 容量须容纳到最后一条保留链接的名次)。
	retained map[string]bool

	// pendingCount 为当前待处理链接数量的增量计数。
	// 待处理数量由该字段直接给出，扫描历史不随调整次数增长。
	pendingCount int
}

func newStore(objectValidFn func(objectID string) bool) *store {
	return &store{
		objectValidFn: objectValidFn,
		links:         make(map[string]*Link),
		buckets:       make(map[BucketKey]*bucket),
		derived:       make(map[string]*DerivedState),
		revoked:       make(map[string]bool),
	}
}

func (s *store) nextSeq() int64 {
	s.seq++
	return s.seq
}

func (s *store) ensureBucket(key BucketKey) *bucket {
	b, ok := s.buckets[key]
	if !ok {
		b = &bucket{
			key:       key,
			baseLimit: Unlimited,
			members:   make(map[string]bool),
			retained:  make(map[string]bool),
		}
		s.buckets[key] = b
	}
	return b
}

// orderedLinks 返回桶内已登记未删除链接按确定性全序
// (seq 升序, 同 seq 时 link ID 字典序) 排列后的链接指针。
func (s *store) orderedLinks(b *bucket) []*Link {
	out := make([]*Link, 0, len(b.members))
	for id := range b.members {
		if lk, ok := s.links[id]; ok && lk.State != StateDeleted {
			out = append(out, lk)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Seq != out[j].Seq {
			return out[i].Seq < out[j].Seq
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// effectiveCapacity 计算桶当前有效容量：
// 至少为 baseLimit；若存在被保留链接，则容量必须扩展到
// 能够容纳全序中最后一条保留链接，且保留链接本身始终占容量。
func (s *store) effectiveCapacity(b *bucket, ordered []*Link) int {
	capacity := b.baseLimit
	if len(b.retained) > 0 {
		lastRetainRank := 0
		for i, lk := range ordered {
			if b.retained[lk.ID] {
				lastRetainRank = i + 1
			}
		}
		if lastRetainRank > capacity {
			capacity = lastRetainRank
		}
	}
	return capacity
}

// objectValid 报告对象当前是否未被撤销。
func (s *store) objectValid(objectID string) bool {
	if s.revoked[objectID] {
		return false
	}
	if s.objectValidFn != nil {
		return s.objectValidFn(objectID)
	}
	return true
}
