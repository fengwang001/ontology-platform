package negotiate

import "strings"

// offer 是服务端支持的一个媒体类型。
type offer struct {
	raw       string
	mediaType string
	subType   string
	params    map[string]string
}

// parseOffer 宽松解析 offer，不校验合法性。
func parseOffer(s string) offer {
	o := offer{raw: s}
	parts := strings.Split(s, ";")
	typ, sub, ok := strings.Cut(strings.ToLower(strings.TrimSpace(parts[0])), "/")
	if ok {
		o.mediaType = strings.TrimSpace(typ)
		o.subType = strings.TrimSpace(sub)
	} else {
		o.mediaType = strings.TrimSpace(strings.ToLower(parts[0]))
	}
	for _, p := range parts[1:] {
		name, value, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok {
			continue
		}
		if o.params == nil {
			o.params = make(map[string]string)
		}
		o.params[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(value)
	}
	return o
}

// match 返回规则匹配该 offer 的具体度，不匹配返回 -1。
// 具体度：精确(2) > 类型通配(1) > 全通配(0)，同级参数多者更具体。
func (r rule) match(o offer) (int, bool) {
	var base int
	switch {
	case r.mediaType == "*":
		base = 0
	case r.mediaType != o.mediaType:
		return 0, false
	case r.subType == "*":
		base = 1
	case r.subType != o.subType:
		return 0, false
	default:
		base = 2
	}
	// q 之前的参数必须全部出现在 offer 中且取值相同。
	for name, value := range r.params {
		if o.params[name] != value {
			return 0, false
		}
	}
	return base*1000 + len(r.params), true
}
