// Package name 定义名字、声明种类与声明本身，不依赖其他包。
package name

// Name 是程序中可声明/可引用的名字，相等判定为逐字节相等。
type Name string

// Valid 报告名字是否非空。
func (n Name) Valid() bool { return n != "" }

// Equal 判定两个名字是否相同。
func (n Name) Equal(o Name) bool { return n == o }

// Kind 是声明的种类，决定该声明是否允许前向引用。
type Kind int

const (
	// KindStrict 不允许前向引用：声明位置之前的引用报「在声明之前使用」。
	KindStrict Kind = iota
	// KindForward 允许前向引用：声明位置之前的引用也能解析到本声明。
	KindForward
)

// Decl 是一条声明：名字、种类、源码位置序号。可比较，逐字段相等即 ==。
type Decl struct {
	Name Name
	Kind Kind
	Pos  int
}
