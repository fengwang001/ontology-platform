package quota

// completed 是 Pod 经默认值补全后的结果：被记账的值。
// 键为基础资源名 X，仅保存补全后仍存在的值；缺省即键不存在。
type completed struct {
	requests map[string]int64
	limits   map[string]int64
}

// complete 按命名空间默认值规则补全 Pod 声明：
//   - 缺省的上限量取默认上限量；
//   - 缺省的请求量，若同一资源已有上限量（声明或补全后）取该上限量，
//     否则取默认请求量；仍缺省则保持缺省；
//   - 已声明的值不被改变。
func complete(spec PodSpec, defs Defaults) completed {
	keys := make(map[string]struct{}, len(spec.Requests)+len(spec.Limits)+len(defs))
	for k := range spec.Requests {
		keys[k] = struct{}{}
	}
	for k := range spec.Limits {
		keys[k] = struct{}{}
	}
	for k := range defs {
		keys[k] = struct{}{}
	}

	out := completed{
		requests: make(map[string]int64, len(keys)),
		limits:   make(map[string]int64, len(keys)),
	}
	for k := range keys {
		rd, hasDef := defs[k]

		limit, hasLimit := spec.Limits[k]
		if !hasLimit && hasDef && rd.Limit != nil {
			limit, hasLimit = *rd.Limit, true
		}

		request, hasRequest := spec.Requests[k]
		if !hasRequest {
			switch {
			case hasLimit:
				request, hasRequest = limit, true
			case hasDef && rd.Request != nil:
				request, hasRequest = *rd.Request, true
			}
		}

		if hasLimit {
			out.limits[k] = limit
		}
		if hasRequest {
			out.requests[k] = request
		}
	}
	return out
}

// value 取补全后资源 r 被记账的量；第二返回值报告是否缺省。
// pods 不属于单资源记账，调用方需单独处理。
func (c completed) value(r ResourceName) (int64, bool) {
	base, ok := r.Base()
	if !ok {
		return 0, false
	}
	if r.IsRequest() {
		v, has := c.requests[base]
		return v, has
	}
	v, has := c.limits[base]
	return v, has
}

// bestEffort 报告补全后是否为尽力型：
// 所有资源的请求量与上限量均缺省或为零。
func (c completed) bestEffort() bool {
	for _, v := range c.requests {
		if v != 0 {
			return false
		}
	}
	for _, v := range c.limits {
		if v != 0 {
			return false
		}
	}
	return true
}
