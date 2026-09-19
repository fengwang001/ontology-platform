package ontology

import "strconv"

// normalize 按 ActionType 的契约校验入参并填入默认值。
// 默认值取自声明时冻结的快照深拷贝，多次调用互不污染。
func normalize(a *ActionType, args map[string]any) (map[string]any, error) {
	var problems ParamErrors

	declared := make(map[string]ParamSpec, len(a.Schema.Params))
	for _, spec := range a.Schema.Params {
		declared[spec.Name] = spec
	}
	for name := range args {
		if _, ok := declared[name]; !ok {
			problems = append(problems, ParamError{
				Name:    name,
				Kind:    ParamUnknown,
				Message: "参数 " + strconv.Quote(name) + " 未在 schema 中声明",
			})
		}
	}

	out := make(map[string]any, len(a.Schema.Params))
	for _, spec := range a.Schema.Params {
		val, present := args[spec.Name]
		if present {
			got, ok := typeName(val, spec.Type)
			if !ok {
				problems = append(problems, ParamError{
					Name:     spec.Name,
					Kind:     ParamTypeMismatch,
					Expected: string(spec.Type),
					Got:      got,
					Message: "参数 " + strconv.Quote(spec.Name) +
						" 类型应为 " + string(spec.Type) + "，实际为 " + got,
				})
				continue
			}
			out[spec.Name] = val
		} else if spec.Required {
			problems = append(problems, ParamError{
				Name:     spec.Name,
				Kind:     ParamMissingRequired,
				Expected: string(spec.Type),
				Message:  "缺少必填参数 " + strconv.Quote(spec.Name),
			})
		} else if d, ok := a.defaultSnapshot[spec.Name]; ok {
			out[spec.Name] = deepCopy(d)
		}
	}

	if len(problems) > 0 {
		return nil, problems
	}
	return out, nil
}
