package endpointshard

// ChangeKind 标识一次变更报告中分片的变化类型。
type ChangeKind int

const (
	// ChangeCreated 分片被新建。
	ChangeCreated ChangeKind = iota + 1
	// ChangeUpdated 分片内容发生变化（端点增删或内容更新）。
	ChangeUpdated
	// ChangeDeleted 分片被删除。
	ChangeDeleted
)

func (k ChangeKind) String() string {
	switch k {
	case ChangeCreated:
		return "created"
	case ChangeUpdated:
		return "updated"
	case ChangeDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

// ShardChange 描述一个被修改的分片。
type ShardChange struct {
	ShardID    int
	Kind       ChangeKind
	Generation uint64
	// Endpoints 是分片在操作结束后的完整内容，按标识字典序；
	// 被删除的分片为 nil。
	Endpoints []Endpoint
}

// ChangeReport 是一次同步或分片大小调整返回的变更报告。
//
// 报告中恰好包含内容发生了任何变化、被新建或被删除的分片，
// 按分片编号升序。每个分片在报告中出现时其修改代次恰好递增一次。
type ChangeReport struct {
	Shards []ShardChange
}

// Empty 报告是否为空（无任何分片被修改）。
func (r ChangeReport) Empty() bool { return len(r.Shards) == 0 }

// QueryResult 是消费者按区域查询的结果。
type QueryResult struct {
	// Endpoints 同区域端点在前，各组内按标识字典序。
	Endpoints []Endpoint
	// Fallback 为 true 表示没有任何就绪端点，结果来自
	// “可服务且终止中”的回退集合（该集合也可能为空）。
	Fallback bool
}

// ShardView 是分片的只读快照，用于观察者自检与测试对照。
type ShardView struct {
	ID         int
	Generation uint64
	Endpoints  []Endpoint
}

// ServiceView 是服务的只读快照。
type ServiceView struct {
	M int
	// LastShardID 已分配的最大分片编号；编号单调递增、永不复用。
	LastShardID int
	Shards      []ShardView
}
