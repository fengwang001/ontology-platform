// Package sorting 实现快递中转场的集袋、封袋、出场与拆袋核对。
//
// 设计目标：
//   - 所有写操作线性化（等价于某个串行顺序），可精确重放；
//   - 加入快件的判定开销为 O(1)，与场内集袋总数、历史快件总数无关；
//   - 查询为只读快照，不会读到中间态。
package sorting

// Category 快件品类。
type Category int

const (
	CategoryNormal  Category = iota // 普通
	CategoryFragile                 // 易碎
	CategoryLiquid                  // 液体
)

// Valid 报告品类是否合法。
func (c Category) Valid() bool {
	return c == CategoryNormal || c == CategoryFragile || c == CategoryLiquid
}

// ConflictsWith 报告两个品类是否互斥（易碎与液体不得同袋）。
func (c Category) ConflictsWith(o Category) bool {
	return (c == CategoryFragile && o == CategoryLiquid) ||
		(c == CategoryLiquid && o == CategoryFragile)
}

// BagStatus 集袋生命周期状态。
type BagStatus int

const (
	BagOpen     BagStatus = iota // 开放中，可加入快件
	BagSealed                    // 已封存（自动或手动），等待出场
	BagDeparted                  // 已出场，内容不可变
	BagUnpacked                  // 已拆袋核对，差异记录已生成
)

func (s BagStatus) String() string {
	switch s {
	case BagOpen:
		return "OPEN"
	case BagSealed:
		return "SEALED"
	case BagDeparted:
		return "DEPARTED"
	case BagUnpacked:
		return "UNPACKED"
	}
	return "UNKNOWN"
}

// Parcel 一件待分拣的快件。
type Parcel struct {
	Waybill  string   // 运单号
	Site     string   // 目的网点编码
	Weight   int64    // 重量（克），合法范围 [1, 1e7]
	Category Category // 品类
}

// MaxParcelWeight 单件重量上限（克）。
const MaxParcelWeight int64 = 10_000_000

// Config 中转场分拣参数。
type Config struct {
	MaxBagCount  int   // 单袋件数上限（>=1）
	MaxBagWeight int64 // 单袋总重上限（克，>=1）
	DwellLimit   int64 // 开放集袋自首件加入起的停留时限（秒，>=0；恰好满即到时）
}

// UnpackResult 拆袋核对结果。缺失与多出清单均按运单号升序。
type UnpackResult struct {
	BagID   int64
	Site    string
	Missing []string // 袋内有而未扫到：标记待查，仍视为在场
	Extra   []string // 扫到而不在袋内：只记录，不入场
}

// BagInfo 集袋只读快照（查询返回的副本，修改不影响场内状态）。
type BagInfo struct {
	ID            int64
	Site          string
	Status        BagStatus
	Waybills      []string // 袋内运单号，按加入顺序
	TotalWeight   int64
	FirstAddTime  int64
	SealTime      int64 // 未封存时为 -1
	DepartTime    int64 // 未出场时为 -1
	TrainNo       string
	UnpackTime    int64 // 未拆袋时为 -1
	UnpackSite    string
	UnpackMissing []string
	UnpackExtra   []string
}
