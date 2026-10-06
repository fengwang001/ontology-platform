package recall

// Warehouse 是药库位置的保留标识。所有病区药柜位置必须为非空字符串，
// 且不得与本标识相同（否则视为药库）。退药只允许回到药库。
const Warehouse = "药库"

// 数量与等级边界。
const (
	minQty   = 1
	maxQty   = 1_000_000
	maxLevel = 3
)

// InboundReq 入库登记：药品、批号、数量进入药库。
type InboundReq struct {
	Now      int64
	DrugID   string
	BatchID  string
	Quantity int
}

// TransferReq 调拨：在两个存放位置之间移动某批次数量。
type TransferReq struct {
	Now      int64
	DrugID   string
	BatchID  string
	From     string
	To       string
	Quantity int
}

// DispenseReq 发放：从某位置向某患者发出某批次数量。
// Consent 为三级召回下的知情确认；其他等级下该字段无作用。
type DispenseReq struct {
	Now      int64
	DrugID   string
	BatchID  string
	Location string
	Patient  string
	Quantity int
	Consent  bool
}

// ReturnReq 退药：患者退回某批次数量，药品只进入药库。
type ReturnReq struct {
	Now      int64
	DrugID   string
	BatchID  string
	Patient  string
	Quantity int
}

// RegisterRecallReq 登记召回：针对一种药品的批号闭区间 [LotLow, LotHigh]
// （字符串字典序，两端含），等级 1..3（越小越严），问题始发时刻 IssueAt。
// RecallID 由调用方给出，必须在系统内唯一。
type RegisterRecallReq struct {
	Now      int64
	RecallID string
	DrugID   string
	LotLow   string
	LotHigh  string
	Level    int
	IssueAt  int64
}

// ReleaseRecallReq 解除召回：只撤销该条登记，不影响其他召回。
type ReleaseRecallReq struct {
	Now      int64
	RecallID string
}

// RecallView 为对外暴露的召回登记快照。
type RecallView struct {
	ID      string
	DrugID  string
	LotLow  string
	LotHigh string
	Level   int
	IssueAt int64
	Active  bool
}

// BatchQueryReq 查询某批次：各位置库存、有效等级、生效召回编号集合。
type BatchQueryReq struct {
	Now     int64
	DrugID  string
	BatchID string
}

// StockAt 为某个位置上的库存数量。
type StockAt struct {
	Location string
	Quantity int
}

// BatchInfo 为批次查询结果。Stock 只列出数量大于 0 的位置。
// EffectiveLevel 为 0 表示当前不受任何召回约束；
// ActiveRecallIDs 只包含达到 EffectiveLevel（最严）的未解除召回编号（并列全列）。
type BatchInfo struct {
	DrugID          string
	BatchID         string
	Stock           []StockAt
	EffectiveLevel  int
	ActiveRecallIDs []string
}

// RecoveryItem 为追回清单中的一条汇总：某患者持有的某批次尚未退回数量。
type RecoveryItem struct {
	Patient string
	BatchID string
	DrugID  string
	Qty     int
}

// RecoveryReq 针对某一条召回生成追回清单。
type RecoveryReq struct {
	Now      int64
	RecallID string
}

// RecoveryResult 为追回清单结果，顺序按 (批次, 患者) 字典序稳定排列。
type RecoveryResult struct {
	RecallID string
	Items    []RecoveryItem
}
