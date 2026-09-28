package aggregation

import (
	"errors"
)

// Op 表示行的操作类型。
type Op int

const (
	OpAdd      Op = 1 // 新增一行
	OpWithdraw Op = 2 // 撤回（删除）一行
)

// Row 是分批到达的一行输入。
type Row struct {
	Op    Op
	RowID string // 行的全局唯一标识
	Group string // 组名
	Value int64  // 用于求和 / 平均的值
	Key   string // 去重计数的成员键
}

// Partial 是本地阶段产出的一个组的部分聚合。
//
// 全零的组（Sum==0 且 Count==0 且 KeyDeltas 为空）不得发送。
type Partial struct {
	Group     string
	Sum       int64
	Count     int64           // 行计数的净增减
	KeyDeltas map[string]int64 // 每个去重键的行数净增减
}

// Metrics 是全局阶段查询到的一组最终指标。
type Metrics struct {
	Sum           int64
	Count         int64
	Avg           float64 // Sum / Count；组不存在或 Count==0 时为 0
	DistinctCount int64
}

// 错误类别：互不相同、可区分的拒绝原因。
var (
	ErrInvalidOp        = errors.New("invalid operation: only add or withdraw allowed")
	ErrEmptyGroupName   = errors.New("invalid group: group name must not be empty")
	ErrWithdrawNotFound = errors.New("withdraw rejected: row id does not exist")
	ErrGroupLimit       = errors.New("group limit exceeded: merge would create too many groups")
	ErrDuplicateRow     = errors.New("duplicate row: row id already exists")
)

// Logger 用于打印每批输入、部分聚合与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

// GlobalAggregator 是并发安全的全局聚合状态。
type GlobalAggregator struct{}

// New 创建全局聚合器。maxGroups<=0 表示不限制组数。
func New(maxGroups int, logger Logger) *GlobalAggregator {
	return &GlobalAggregator{}
}

// Submit 提交一批行：本地预聚合后并入全局状态。
// 校验失败时整体拒绝，全局状态与已发送计数不变。
func (g *GlobalAggregator) Submit(rows []Row) error {
	_ = rows
	return nil
}

// Get 查询单个组的指标；组不存在时第二个返回值为 false。
func (g *GlobalAggregator) Get(group string) (Metrics, bool) {
	return Metrics{}, false
}

// Groups 返回当前所有组名（排序后）。
func (g *GlobalAggregator) Groups() []string {
	return nil
}

// SubmittedBatches 返回已接受的批次数。
func (g *GlobalAggregator) SubmittedBatches() int64 { return 0 }

// SentPartials 返回本地阶段发送给全局阶段的部分聚合（组）总数。
func (g *GlobalAggregator) SentPartials() int64 { return 0 }

// AggregateLocal 仅执行本地部分聚合（不做校验与状态合并），供测试与复用。
func AggregateLocal(rows []Row) []Partial {
	return nil
}
