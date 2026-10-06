package gengc

// Generation 标识对象当前所在区。
type Generation int

const (
	GenYoung Generation = 0
	GenOld   Generation = 1
)

func (g Generation) String() string {
	if g == GenOld {
		return "old"
	}
	return "young"
}

// Object 是一个托管对象：固定数量的引用槽（字段），编号 [0, NumFields)。
// 槽值为 0 表示空引用。
type Object struct {
	id         uint64
	gen        Generation
	fields     []uint64
	youngGCs   int
	promotions int
}

// ID 返回对象标识。
func (o *Object) ID() uint64 { return o.id }

// Gen 返回对象当前所在区。
func (o *Object) Gen() Generation { return o.gen }

// NumFields 返回引用字段数量。
func (o *Object) NumFields() int { return len(o.fields) }

// YoungGCs 返回对象经历过的年轻区回收次数（晋升失败后钳制在阈值）。
func (o *Object) YoungGCs() int { return o.youngGCs }

// Promotions 返回该对象的晋升次数（0 或 1）。
func (o *Object) Promotions() int { return o.promotions }

// Field 返回第 i 个引用槽的当前值（调用方须完成存活校验）。
func (o *Object) Field(i int) uint64 { return o.fields[i] }

func (o *Object) clone() *Object {
	c := *o
	c.fields = append([]uint64(nil), o.fields...)
	return &c
}
