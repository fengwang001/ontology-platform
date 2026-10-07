package precheck

// 本文件提供预检演算中的“只读冻结”保证：
//   - 入参在进入演算前被深拷贝，钩子对 map/slice 的任何修改都不外泄；
//   - 后置阶段通过 Scratch 读到的也是前置快照的深拷贝，无法回写；
//   - 对象视图只有读接口，引擎在预检路径上从不调用任何写存储的方法。

func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = deepCopy(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = deepCopy(x[i])
		}
		return out
	case []string:
		return append([]string(nil), x...)
	case map[string]string:
		out := make(map[string]string, len(x))
		for k, val := range x {
			out[k] = val
		}
		return out
	default:
		return v
	}
}

func deepCopyParams(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = deepCopy(v)
	}
	return out
}
