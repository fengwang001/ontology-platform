package cors

import "strings"

// normalizeHeaderName 把头名字规范化为小写；缓存与校验一律使用该形式。
func normalizeHeaderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// isTokenChar 报告 b 是否为 RFC 7230 token 字符（合法头名字字符）。
func isTokenChar(b byte) bool {
	switch {
	case '0' <= b && b <= '9', 'a' <= b && b <= 'z', 'A' <= b && b <= 'Z':
		return true
	}
	switch b {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isTokenChar(name[i]) {
			return false
		}
	}
	return true
}

func invalidArg(msg string) *Error {
	return &Error{Code: ErrCodeInvalidArgument, Msg: msg}
}

// validateRequest 校验请求参数；任何非法都在改动缓存与时钟之前报出。
func validateRequest(req Request) error {
	if req.Origin == "" {
		return invalidArg("empty origin")
	}
	if req.Target == "" {
		return invalidArg("empty target")
	}
	if req.Method == "" {
		return invalidArg("empty method")
	}
	for _, h := range req.Headers {
		if !validHeaderName(h.Name) {
			return invalidArg("illegal header name: " + h.Name)
		}
	}
	return nil
}

// charsetOK 报告值是否全部落在允许字符集内；nil 谓词表示不限制。
func charsetOK(value string, allowed func(byte) bool) bool {
	if allowed == nil {
		return true
	}
	for i := 0; i < len(value); i++ {
		if !allowed(value[i]) {
			return false
		}
	}
	return true
}

// classify 判定请求是否为简单请求，并列出非安全头（规范化、去重、保持出现顺序）。
//
// 简单请求当且仅当三条同时成立：方法在安全方法集合内；每个头都在安全头
// 集合内；每个头的值满足该头的值约束（长度上限与允许字符集）。
// 非安全头按名字判定（不在安全头集合内），与值约束无关。
//
// 性能：安全方法与安全头均为哈希表查找，开销只随请求自身的头数量增长，
// 不随安全头集合之外的配置规模增长。
func (k *Kernel) classify(req Request) (simple bool, nonSafe []string) {
	_, methodOK := k.safeMethods[req.Method]
	simple = methodOK
	seen := make(map[string]bool)
	for _, h := range req.Headers {
		name := normalizeHeaderName(h.Name)
		rule, ok := k.safeHeaders[name]
		if !ok {
			if !seen[name] {
				seen[name] = true
				nonSafe = append(nonSafe, name)
			}
			simple = false
			continue
		}
		if len(h.Value) > rule.MaxLen || !charsetOK(h.Value, rule.Allowed) {
			simple = false
		}
	}
	return simple, nonSafe
}
