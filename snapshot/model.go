package snapshot

// Record 是本体对象在快照中的通用表示。
// ID 在同一对象类型内唯一；Refs 为跨类型链接字段（引用）。
type Record struct {
	ID   string         `json:"id"`
	Data []byte         `json:"data"`
	Refs []CrossTypeRef `json:"refs,omitempty"`
}

// CrossTypeRef 是一条指向另一对象类型块的跨类型引用。
type CrossTypeRef struct {
	Field      string `json:"field"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
}

// Header 是每个分块文件的块头。
type Header struct {
	Type          string `json:"type"`
	ChunkIndex    int    `json:"chunk_index"`
	DeclaredCount int    `json:"declared_count"`
	ChecksumAlgo  string `json:"checksum_algo"`
}

// Envelope 是单个分块文件的磁盘布局。
type Envelope struct {
	Header   Header   `json:"header"`
	Records  []Record `json:"records"`
	Checksum string   `json:"checksum"`
}

// Manifest 描述一次导出包含哪些类型。
type Manifest struct {
	Types []string `json:"types"`
}

// IssueKind 是可区分的错误类别。
type IssueKind string

const (
	KindOutOfRange       IssueKind = "out_of_range"
	KindIntegrityFailure IssueKind = "integrity_failure"
	KindCountMismatch    IssueKind = "count_mismatch"
	KindDanglingRef      IssueKind = "dangling_reference"
	KindRefUnverifiable  IssueKind = "reference_unverifiable"
)

// ChunkStatus 是单个块独立校验后的可信状态。
type ChunkStatus string

const (
	StatusTrusted       ChunkStatus = "trusted"
	StatusIntegrityBad  ChunkStatus = "integrity_failed"
	StatusCountMismatch ChunkStatus = "count_mismatch"
)

// ChunkRef 定位一个块。
type ChunkRef struct {
	Type  string
	Chunk int
}

// Issue 是一次加载/聚合命中的单条问题，类别互斥、不得混报。
type Issue struct {
	Kind   IssueKind
	Chunk  ChunkRef
	Reason string
	Ref    *CrossTypeRef
}

// Decision 记录一次判定的输入、输出与依据，供日志使用。
type Decision struct {
	Stage  string
	Input  string
	Output string
	Basis  string
	Chunk  *ChunkRef
}

// DecisionLogger 接收每次判定的结构化日志。
type DecisionLogger interface {
	Log(Decision)
}

// ChunkReport 是单个块独立校验的结论。
type ChunkReport struct {
	Chunk    ChunkRef
	Status   ChunkStatus
	Declared int
	Actual   int
	Records  []Record
	Reason   string
}

// LoadResult 是一次加载请求的完整判定结果。
type LoadResult struct {
	Reports map[ChunkRef]*ChunkReport
	Issues  []Issue
}

// AggregateResult 是聚合视图结果。
type AggregateResult struct {
	Aggregatable bool
	Issues       []Issue
	Objects      map[string][]Record
}
