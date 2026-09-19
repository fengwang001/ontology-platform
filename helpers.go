package ontology

// typeName 校验值是否匹配声明类型，返回实际类型描述。
func typeName(v any, want ParamType) (string, bool) {
	switch want {
	case TypeString:
		_, ok := v.(string)
		return "non-string", ok
	case TypeInt:
		_, ok := v.(int)
		return "non-int", ok
	case TypeBool:
		_, ok := v.(bool)
		return "non-bool", ok
	case TypeFloat:
		_, ok := v.(float64)
		return "non-float64", ok
	default:
		return "unknown-declared-type", false
	}
}

// deepCopy 复制 map/slice 类默认值，防止多次调用间共享底层数据。
func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		c := make(map[string]any, len(t))
		for k, sub := range t {
			c[k] = deepCopy(sub)
		}
		return c
	case []any:
		c := make([]any, len(t))
		for i, sub := range t {
			c[i] = deepCopy(sub)
		}
		return c
	default:
		return v
	}
}
