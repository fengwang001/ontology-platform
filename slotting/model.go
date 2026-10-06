// Package slotting 实现入库上架的货位分配系统。
//
// 子模块职责：
//   - model.go：领域类型（货位坐标、货位配置、托盘、品类、状态）。
//   - errors.go：可区分的业务错误及拒绝优先级。
//   - constraints.go：纯函数式约束校验（承重/净高/品类/混放/相邻隔离）。
//   - treap.go：确定性有序集合，用于按字典序取候选货位。
//   - store.go：并发安全的占用账与候选索引。
//   - service.go：对外操作（自动/指定上架、移库、取出、冻结、批量、查询）。
//   - logging.go：记录输入、输出与判定依据的日志装饰器。
package slotting

// coordLess 定义货位坐标的字典序：通道号、层号、位序号。
func coordLess(a, b Coord) bool {
	if a.Aisle != b.Aisle {
		return a.Aisle < b.Aisle
	}
	if a.Level != b.Level {
		return a.Level < b.Level
	}
	return a.Position < b.Position
}

// Category 为托盘品类。
type Category int

const (
	CategoryNormal    Category = iota // 普通
	CategoryFood                      // 食品
	CategoryFlammable                 // 易燃
)

func (c Category) Valid() bool { return c >= CategoryNormal && c <= CategoryFlammable }

// LocationStatus 为货位状态。
type LocationStatus int

const (
	StatusNormal LocationStatus = iota // 正常
	StatusFrozen                       // 冻结
)

// Coord 由通道号、层号、位序号唯一确定一个货位。
type Coord struct {
	Aisle    int
	Level    int
	Position int
}

// LocationConfig 为货位静态属性。
type LocationConfig struct {
	Coord           Coord
	WeightLimit     int               // 承重上限（千克），正整数
	ClearHeight     int               // 净高（厘米），正整数
	Allowed         map[Category]bool // 允许品类集合
	Capacity        int               // 容纳托盘数，1 或 2
	AllowMixedBatch bool              // 是否允许混批
	Status          LocationStatus    // 正常 / 冻结
}

// Pallet 为托盘属性，数值字段均为正整数。
type Pallet struct {
	ID       string
	Product  string
	Batch    string
	Category Category
	Weight   int
	Height   int
}

// LocationView 为某货位的只读一致快照。
type LocationView struct {
	Coord           Coord
	WeightLimit     int
	ClearHeight     int
	Allowed         []Category
	Capacity        int
	Occupied        int
	AllowMixedBatch bool
	Status          LocationStatus
	PalletIDs       []string
	RemainingWeight int // 剩余承重
}
