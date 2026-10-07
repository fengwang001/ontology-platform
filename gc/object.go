package gc

// ObjID 是托管对象的句柄，NilObj 表示空引用（可写入字段用于清除引用）。
type ObjID uint64

const NilObj ObjID = 0

type generation uint8

const (
	genYoung generation = iota
	genOld
)

func (g generation) String() string {
	if g == genOld {
		return "old"
	}
	return "young"
}

// object 是对象与引用登记模块的核心记录；对象被回收后记录保留（alive=false），
// 以便区分「从未存在」（未定义错误）与「曾经存在但已回收」（悬垂引用错误）。
type object struct {
	id     ObjID
	gen    generation
	fields []ObjID
	age    int // 已熬过的年轻区回收次数
	alive  bool
}
