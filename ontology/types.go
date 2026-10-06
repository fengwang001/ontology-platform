// Package ontology 实现合租住户之间的共同费用分账与净额清算服务。
package ontology

import "sync"

// SplitMethod 决定账单在当天在住住户之间的分摊方式。
type SplitMethod int

const (
	// SplitPerHead 按人头均摊。
	SplitPerHead SplitMethod = iota
	// SplitByArea 按房间面积加权分摊。
	SplitByArea
)

// BillStatus 描述账单的生命周期状态。
type BillStatus int

const (
	// BillActive 正常计入净额。
	BillActive BillStatus = iota
	// BillDisputed 争议中，暂时从净额剔除。
	BillDisputed
	// BillAdjudicated 已裁定，按裁定金额归属。
	BillAdjudicated
)

// Segment 是某住户在某房间连续在住的左闭右开区间 [start, end)。
// end 为 0 表示当前仍在住（未退出，区间向右无限延伸）。
type Segment struct {
	ResidentID int64
	Room       int64
	Start      int64
	End        int64 // 0 表示开放区间
}

// Resident 是一名住户的档案。
type Resident struct {
	ID       int64
	Segments []Segment
	Active   bool
	OutDay   int64
}

// Contribution 是某一账单中某住户最终承担的整数份额（0 表示房东承担槽位）。
type Contribution struct {
	ResidentID int64
	Amount     int64
}

// Bill 是一张共同账单的完整记录。
type Bill struct {
	ID           int64
	Amount       int64
	StartDay     int64
	EndDay       int64
	Method       SplitMethod
	PayerID      int64 // >0 表示垫付住户，landlordPayer 表示房东代收
	EntryDay     int64
	Status       BillStatus
	Disputed     bool
	Adjudication int64
	Contribs     []Contribution // 当前生效归属（含房东槽位）
	OrigContribs []Contribution // 争议前归属快照
	OrigAmount   int64
	Seq          int64 // 录入操作的全局序号
}

// Settlement 是一次退出清算或补充清算记录。
type Settlement struct {
	ResidentID int64
	AtDay      int64
	Suppl      bool
	Reason     string
	Lines      []SettlementLine
}

// SettlementLine 是清算记录中该住户与另一名住户之间的净额（有向，residentID 视角）。
type SettlementLine struct {
	OtherID int64
	Amount  int64
}

// Service 是分账清算服务的全部可变状态。
type Service struct {
	mu            sync.RWMutex
	lastNow       int64
	clockInit     bool
	maxAmount     int64
	disputeWindow int64
	residents     map[int64]*Resident
	bills         map[int64]*Bill
	roomAreas     map[int64]int64
	// net 以规范无向对为键保存当前净额（正表示 key.a 应收 key.b）。
	net map[pairKey]int64
	// folded 为某住户“已进入清算头寸”的累计有向贡献，按住户分别保存。
	folded map[int64]map[int64]int64
	// included 记录已退出住户对每张账单的贡献是否已并入 folded。
	included map[int64]map[int64]bool
	// settled 标记住户是否已退出清算。
	settled     map[int64]bool
	settlements map[int64][]Settlement
	seq         int64
	outSeq      map[int64]int64
}

// NewService 创建服务，maxAmount 为账单/裁定金额上界，disputeWindow 为可争议天数 D。
func NewService(maxAmount int64, disputeWindow int64) *Service {
	return &Service{
		maxAmount:     maxAmount,
		disputeWindow: disputeWindow,
		residents:     map[int64]*Resident{},
		bills:         map[int64]*Bill{},
		roomAreas:     map[int64]int64{},
		net:           map[pairKey]int64{},
		folded:        map[int64]map[int64]int64{},
		included:      map[int64]map[int64]bool{},
		settled:       map[int64]bool{},
		settlements:   map[int64][]Settlement{},
		outSeq:        map[int64]int64{},
	}
}
