// Package slot 保存拣选位与储备库存的纯数据状态（不含锁、不含任务规则）。
package slot

import "errors"

var (
	// ErrInvalid 参数非法（拒绝次序中最先判定）。
	ErrInvalid = errors.New("slot: invalid argument")
	// ErrNotFound 库位或 SKU 不存在。
	ErrNotFound = errors.New("slot: not found")
	// ErrConflict 库位编号重复。
	ErrConflict = errors.New("slot: location conflict")
	// ErrShort Pick 数量超过拣选位现有量。
	ErrShort = errors.New("slot: insufficient on-hand stock")
)

// Slot 为一个拣选位的全部状态。任务相关聚合量增量维护，避免全量扫描。
type Slot struct {
	Loc       string
	SKU       string
	Min       int64
	Max       int64
	Cap       int64
	Case      int64
	OnHand    int64
	InTransit int64 // 该库位全部未完成任务量之和
	UrgentQty int64 // 该库位未完成紧急任务量之和
	Starved   int64 // 想补但储备不足整箱的次数
	OpenTasks []int64
}

// World 为全部库位与储备库存。
type World struct {
	slots   map[string]*Slot
	bySKU   map[string][]*Slot
	reserve map[string]int64
}

// NewWorld 创建空世界。
func NewWorld() *World {
	return &World{
		slots:   make(map[string]*Slot),
		bySKU:   make(map[string][]*Slot),
		reserve: make(map[string]int64),
	}
}

// Get 按编号取库位。
func (w *World) Get(loc string) (*Slot, bool) {
	s, ok := w.slots[loc]
	return s, ok
}

// HasSKU 报告该 SKU 是否已有任何拣选位。
func (w *World) HasSKU(sku string) bool { return len(w.bySKU[sku]) > 0 }

// Reserve 返回 SKU 储备库存。
func (w *World) Reserve(sku string) int64 { return w.reserve[sku] }

// Put 注册一个新库位。
func (w *World) Put(s *Slot) {
	w.slots[s.Loc] = s
	w.bySKU[s.SKU] = append(w.bySKU[s.SKU], s)
}

// AddReserve 给储备库存直接加量（调用方负责校验）。
func (w *World) AddReserve(sku string, qty int64) { w.reserve[sku] += qty }

// SubReserve 从储备库存直接减量。
func (w *World) SubReserve(sku string, qty int64) { w.reserve[sku] -= qty }
