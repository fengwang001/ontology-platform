package ontology

import "fmt"

// KeyTypeError 表示同名连接键在左右两侧出现了不可比较的类型。
// 例如左侧为 string、右侧为 int64。可通过 errors.As 判定。
type KeyTypeError struct {
	Key       string // 发生冲突的连接键名
	LeftType  string // 左侧该键值的 Go 类型
	RightType string // 右侧该键值的 Go 类型
}

func (e *KeyTypeError) Error() string {
	return fmt.Sprintf("join key %q: incomparable types %s (left) vs %s (right)",
		e.Key, e.LeftType, e.RightType)
}

// UnsupportedKeyTypeError 表示连接键上出现了不支持的类型。
// 连接键只支持 string、bool、int、int64、float64（以及缺失/nil）。
type UnsupportedKeyTypeError struct {
	Key  string // 连接键名
	Side string // "left" 或 "right"
	Type string // 实际值的 Go 类型
}

func (e *UnsupportedKeyTypeError) Error() string {
	return fmt.Sprintf("join key %q: unsupported type %s on %s side",
		e.Key, e.Type, e.Side)
}
