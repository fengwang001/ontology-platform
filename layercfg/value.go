package layercfg

// NewString / NewInt / NewBool / NewStringList 为常用值构造器。

func NewString(s string) Value {
	return Value{Type: TypeString, Str: s}
}

func NewInt(n int64) Value {
	return Value{Type: TypeInt, Int: n}
}

func NewBool(b bool) Value {
	return Value{Type: TypeBool, Bool: b}
}

func NewStringList(items []string) Value {
	// 复制一份，避免调用方后续修改切片影响不可变值。
	cp := append([]string(nil), items...)
	return Value{Type: TypeStringList, List: cp}
}

// checkValue 按模式校验值的类型与整数取值范围。
func checkValue(schema Schema, v Value) error {
	if v.Type != schema.Type {
		return errorf(ErrTypeOrRange, "key %q expects value type %d, got %d", schema.Key, schema.Type, v.Type)
	}
	if v.Type == TypeInt && schema.hasRange && (v.Int < schema.Min || v.Int > schema.Max) {
		return errorf(ErrTypeOrRange, "key %q int value %d out of range [%d,%d]", schema.Key, v.Int, schema.Min, schema.Max)
	}
	return nil
}

// cloneValue 复制值中唯一的引用类型字段，保证快照不可变。
func cloneValue(v Value) Value {
	if v.List != nil {
		v.List = append([]string(nil), v.List...)
	}
	return v
}

// equalValue 比较两个值是否相等（用于无变更发布与测试）。
func equalValue(a, b Value) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case TypeString:
		return a.Str == b.Str
	case TypeInt:
		return a.Int == b.Int
	case TypeBool:
		return a.Bool == b.Bool
	case TypeStringList:
		if len(a.List) != len(b.List) {
			return false
		}
		for i := range a.List {
			if a.List[i] != b.List[i] {
				return false
			}
		}
		return true
	default:
		return true
	}
}
