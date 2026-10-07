package quota

// EffectivePod 是补全后的 Pod 记账视图。
// Requests/Limits 中缺失的键表示该资源仍缺省（不是 0）。
type EffectivePod struct {
	Requests    map[string]int64
	Limits      map[string]int64
	Terminating bool
}

// defaultPod 按命名空间默认值规则补全 Pod：
//   - 缺省的上限量取默认上限量；
//   - 缺省的请求量，若同一资源已有上限量（声明或补全后）则取该上限量，
//     否则取默认请求量；
//   - 仍然缺省则保持缺省。
//
// 已声明的值不被改变；补全结果即被记账的值。
func defaultPod(spec PodSpec, def Defaults) EffectivePod {
	names := make(map[string]struct{}, len(spec.Requests)+len(spec.Limits)+len(def.Rules))
	for x := range spec.Requests {
		names[x] = struct{}{}
	}
	for x := range spec.Limits {
		names[x] = struct{}{}
	}
	for x := range def.Rules {
		names[x] = struct{}{}
	}

	eff := EffectivePod{
		Requests:    make(map[string]int64, len(names)),
		Limits:      make(map[string]int64, len(names)),
		Terminating: spec.DeadlineSeconds != nil && *spec.DeadlineSeconds > 0,
	}
	for x := range names {
		limit, hasLimit := spec.Limits[x]
		if !hasLimit {
			if rule, ok := def.Rules[x]; ok && rule.Limit != nil {
				limit, hasLimit = *rule.Limit, true
			}
		}
		req, hasReq := spec.Requests[x]
		if !hasReq {
			switch {
			case hasLimit:
				req, hasReq = limit, true
			default:
				if rule, ok := def.Rules[x]; ok && rule.Request != nil {
					req, hasReq = *rule.Request, true
				}
			}
		}
		if hasLimit {
			eff.Limits[x] = limit
		}
		if hasReq {
			eff.Requests[x] = req
		}
	}
	return eff
}
