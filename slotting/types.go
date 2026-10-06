// Package slotting 实现仓库入库上架的货位分配系统。
//
// 模块划分：
//   - types.go：领域类型（货位、托盘、查询视图）
//   - errors.go：可区分的拒绝原因
//   - ledger.go：状态台账（货位、托盘、在位关系、商品→货位索引）
//   - index.go：空货位的 sqrt 分块索引，保证自动上架的考察货位数次线性
//   - check.go：单点与相邻约束的纯函数判定
//   - system.go：对外线程安全外观（加锁、快照查询、日志）
//   - ops.go：锁内操作（自动/指定上架、移库、取出、冻结、批量）
package slotting

import "fmt"

// Category 为品类。
type Category string

const (
	CatGeneral   Category = "普通"
	CatFood      Category = "食品"
	CatFlammable Category = "易燃"
)

// LocationID 由通道号、层号、位序号唯一确定。
type LocationID struct {
	Aisle int
	Level int
	Index int
}

// Less 按 (通道号, 层号, 位序号) 字典序比较。
func (l LocationID) Less(o LocationID) bool {
	if l.Aisle != o.Aisle {
		return l.Aisle < o.Aisle
	}
	if l.Level != o.Level {
		return l.Level < o.Level
	}
	return l.Index < o.Index
}

// String 返回稳定的文本表示。
func (l LocationID) String() string {
	return fmt.Sprintf("(通道%d,层%d,位%d)", l.Aisle, l.Level, l.Index)
}

// Adjacent 判断两个货位是否相邻：同一通道同一层、位序号相差 1。
func (l LocationID) Adjacent(o LocationID) bool {
	if l == o {
		return false
	}
	d := l.Index - o.Index
	if d < 0 {
		d = -d
	}
	return l.Aisle == o.Aisle && l.Level == o.Level && d == 1
}

// LocationStatus 为货位状态。
type LocationStatus string

const (
	StatusNormal LocationStatus = "正常"
	StatusFrozen LocationStatus = "冻结"
)

// Location 为货位静态属性。
type Location struct {
	ID          LocationID
	MaxWeight   int        // 承重上限（千克），>0
	ClearHeight int        // 净高（厘米），>0
	Allowed     []Category // 允许品类集合
	Capacity    int        // 容纳托盘数（1 或 2）
	AllowMix    bool       // 是否允许混批
	Status      LocationStatus
}

// Pallet 为到货托盘。所有字段均为正整数（品类为枚举）。
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
	ID             LocationID
	Status         LocationStatus
	Capacity       int
	UsedCapacity   int
	RemainCapacity int
	MaxWeight      int
	UsedWeight     int
	RemainWeight   int
	ClearHeight    int
	PalletIDs      []string
}
