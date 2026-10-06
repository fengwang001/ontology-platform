package endpointshard

// shard 是一个分片：编号、修改代次与成员端点标识集合。
// 端点数据本身只在 Service.endpoints 中保存一份。
type shard struct {
	id      int
	gen     uint64
	members map[string]struct{}
}

func newShard(id int) *shard {
	return &shard{id: id, members: make(map[string]struct{})}
}

func (s *shard) count() int { return len(s.members) }

// placeKey 是落位索引的键：端点数最多但未满的分片优先，并列取编号小者。
// 通过 (负端点数, 编号) 升序实现，最小键即最优落位目标。
type placeKey struct {
	negCount int
	id       int
}

func lessPlaceKey(a, b placeKey) bool {
	if a.negCount != b.negCount {
		return a.negCount < b.negCount
	}
	return a.id < b.id
}

// countKey 是整理（合并候选）索引的键：(端点数, 编号) 升序，
// 最小两个键即端点数最少的两个分片（并列取编号小者）。
type countKey struct {
	count int
	id    int
}

func lessCountKey(a, b countKey) bool {
	if a.count != b.count {
		return a.count < b.count
	}
	return a.id < b.id
}

func lessString(a, b string) bool { return a < b }
