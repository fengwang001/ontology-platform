package ontology

import (
	"sync"
	"time"
)

// 审计日志记录的操作类型。
const (
	OpRead         = "read"
	OpReadAt       = "read_at"
	OpWrite        = "write"
	OpTagsOf       = "tags_of"
	OpRegisterType = "register_object_type"
	OpSetRule      = "set_tag_rule"
	OpDeleteRule   = "delete_tag_rule"
	OpSetGrant     = "set_grant"
)

// 审计日志记录的裁决结论。
const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"
	DecisionError = "error"
)

// TagBasis 记录一次裁决所依据的单个标签的判定结论。
// 只记录标签级的布尔结论与授权结论，绝不记录属性取值，
// 因此日志本身不会向无权主体泄露其不可读属性的信息。
type TagBasis struct {
	Tag       string // 标签名
	Carried   bool   // 实例在判定版本上是否携带该标签
	Consulted bool   // 是否查询了授权表（仅当携带时）
	Allowed   bool   // 该标签对本次操作的授权结论（未咨询时视为允许）
}

// Entry 是一次调用的完整审计记录：输入、最终输出与据以裁决的标签判定依据。
type Entry struct {
	Seq        uint64    // 全局单调递增序号
	Time       time.Time // 调用时间（来自注入时钟）
	Op         string    // 操作类型
	Principal  string    // 发起主体
	Instance   string    // 目标实例（type/id）或配置对象名
	Attributes []string  // 请求的属性（排序后）
	SnapshotID uint64    // 可重复读快照 ID（非快照读为 0）
	Version    uint64    // 读取所基于的版本 / 写入产生的版本
	Decision   string    // allow / deny / error
	ErrClass   string    // 错误类别（无错误时为空）
	Basis      []TagBasis
	AttrLoads  int // 本次判定实际读取的属性数量（可观测的开销证明）
}

// Logger 接收审计日志。实现必须是并发安全的。
type Logger interface {
	Log(Entry)
}

type discardLogger struct{}

func (discardLogger) Log(Entry) {}

// MemoryLogger 把审计日志保存在内存中，供测试与本地验证使用。
type MemoryLogger struct {
	mu      sync.Mutex
	entries []Entry
}

func NewMemoryLogger() *MemoryLogger { return &MemoryLogger{} }

func (m *MemoryLogger) Log(e Entry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, e)
}

// Entries 返回目前已记录的全部日志（按记录顺序）。
func (m *MemoryLogger) Entries() []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Entry, len(m.entries))
	copy(out, m.entries)
	return out
}
