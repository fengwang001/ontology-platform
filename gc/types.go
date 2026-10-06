package gc

import "time"

// Policy 是删除策略。
type Policy string

const (
	// Background 后台删除：属主移除后，失去全部属主的依赖者以后台策略连带删除。
	Background Policy = "Background"
	// Foreground 前台删除：属主等待阻塞型存活依赖者消失后才被移除，
	// 且会把满足条件的依赖者以前台策略连带删除。
	Foreground Policy = "Foreground"
	// Orphan 孤立删除：属主被移除后依赖者保留（成为孤儿）。
	Orphan Policy = "Orphan"
)

// valid 报告策略是否合法。
func (p Policy) valid() bool {
	return p == Background || p == Foreground || p == Orphan
}

// OwnerRef 是一条属主引用：指向属主对象，并带有是否阻塞属主删除的标志。
type OwnerRef struct {
	OwnerID string
	Block   bool
}

// object 是控制器内部保存的对象状态。
type object struct {
	id         string
	owners     map[string]bool // ownerID -> block
	finalizers []string
	deleting   bool
	policy     Policy
	reqTime    time.Time

	// 用于 O(受影响对象) 级联的计数器，均由 settle 与属主修改维护：
	liveOwners   int // 存在且未处于删除中的属主数量
	fgOwners     int // 存在且处于前台删除中的属主数量
	blockingDeps int // 存活（未删除中）且 Block=true 的依赖者数量
}

// View 是对象状态的只读快照，供观测与测试使用。
type View struct {
	ID         string
	Owners     []OwnerRef // 按 OwnerID 升序
	Finalizers []string   // 按追加顺序
	Deleting   bool
	Policy     Policy
	ReqTime    time.Time
}
