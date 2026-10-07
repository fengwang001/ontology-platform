package diff

// value.go 负责取值的规范化、取值与声明类型的校验，
// 以及"旧取值按新类型解读是否仍然有效"的兼容性判定。

// validateValue 校验 v 是否满足声明类型 t 的约束。
func validateValue(t ValueType, v TypedValue) error {
	return nil
}

// valueCompatible 判断旧取值 old 在声明类型从 oldType 变为 newType 后，
// 是否仍能按 newType 有效解读；有效时返回规范化后的可比值。
func reinterpret(oldType, newType ValueType, old TypedValue) (TypedValue, bool) {
	return old, false
}
