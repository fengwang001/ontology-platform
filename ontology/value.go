package ontology

// Value 表示一个属性值。Present=false 表示"属性当前不存在"，
// 与显式写入一个等于默认值的值（Present=true, Data 为零值）严格区分。
type Value struct {
	Present bool
	Data    string
}

// Absent 是"属性当前不存在"的特殊标记值。
var Absent = Value{Present: false}

// Of 构造一个显式存在的属性值。
func Of(s string) Value { return Value{Present: true, Data: s} }

// Key 是索引键。AbsentKey 是"不存在"状态在索引中的保留键，
// 保证不存在状态与任何显式值（包括默认值 ""）在索引中可区分。
type Key struct {
	Absent bool
	S      string
}

// AbsentKey 是不存在状态的保留索引键。
var AbsentKey = Key{Absent: true}

// KeyOf 为显式值生成普通索引键。
func KeyOf(s string) Key { return Key{S: s} }

// KeyFunc 是声明被索引属性时指定的索引键生成规则。
// 输入属性值（含 Absent 标记），输出该值在某个索引结构中的键。
type KeyFunc func(Value) Key
