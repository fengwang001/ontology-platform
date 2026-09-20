package join

import (
	"errors"
	"fmt"
)

// ErrKeyTypeConflict 表示同一连接键两侧（或同侧内部）出现了不可比较的类型组合，
// 例如一侧是 string、另一侧是 int64。可用 errors.Is 判定。
var ErrKeyTypeConflict = errors.New("join: key type conflict")

// ErrUnsupportedKeyType 表示连接键的值类型不在受支持集合
// （string、bool、int、int64、float64）之内。可用 errors.Is 判定。
var ErrUnsupportedKeyType = errors.New("join: unsupported key type")

// ErrNoKeys 表示 Join 被调用时没有给出任何连接键。
var ErrNoKeys = errors.New("join: at least one join key is required")

// KeyTypeError 描述某个连接键上的类型问题，指明键名与冲突两侧的类型。
// LeftType / RightType 为 Go 类型名（如 "string"、"int64"）；
// 若冲突发生在同一侧内部，SameSide 为 true，两个字段是该侧看到的两种类型。
type KeyTypeError struct {
	Key         string
	LeftType    string
	RightType   string
	SameSide    bool
	Unsupported bool
}

func (e *KeyTypeError) Error() string {
	if e.Unsupported {
		return fmt.Sprintf("%v: key %q has unsupported type %q", ErrUnsupportedKeyType, e.Key, e.LeftType)
	}
	if e.SameSide {
		return fmt.Sprintf("%v: key %q mixes incompatible types %q and %q on the same side", ErrKeyTypeConflict, e.Key, e.LeftType, e.RightType)
	}
	return fmt.Sprintf("%v: key %q is %q on the left but %q on the right", ErrKeyTypeConflict, e.Key, e.LeftType, e.RightType)
}

// Is 使 errors.Is(err, ErrKeyTypeConflict / ErrUnsupportedKeyType) 成立。
func (e *KeyTypeError) Is(target error) bool {
	if e.Unsupported {
		return target == ErrUnsupportedKeyType
	}
	return target == ErrKeyTypeConflict
}
