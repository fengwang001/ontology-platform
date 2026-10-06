package cors

import "sort"

// compiledRule 是预编译后的安全头值约束。
type compiledRule struct {
	maxLength int
	allowed   [256]bool
}

// classifier 负责请求分类。所有集合都用哈希表，
// 单次判定开销与安全头集合之外的配置规模无关。
type classifier struct {
	safeMethods   map[string]struct{}
	safeHeaders   map[string]compiledRule
	safeResponses map[string]struct{}
}

func newClassifier(cfg Config) (*classifier, *Error) {
	c := &classifier{
		safeMethods:   make(map[string]struct{}, len(cfg.SafeMethods)),
		safeHeaders:   make(map[string]compiledRule, len(cfg.SafeHeaders)),
		safeResponses: make(map[string]struct{}, len(cfg.SafeResponseHeaders)),
	}
	for _, m := range cfg.SafeMethods {
		if m == "" {
			return nil, invalidArg("safe method must not be empty")
		}
		c.safeMethods[m] = struct{}{}
	}
	for name, rule := range cfg.SafeHeaders {
		nn, ok := normalizeHeaderName(name)
		if !ok {
			return nil, invalidArg("invalid safe header name %q", name)
		}
		if rule.MaxLength <= 0 {
			return nil, invalidArg("safe header %q max length must be positive", name)
		}
		cr := compiledRule{maxLength: rule.MaxLength}
		for i := 0; i < len(rule.AllowedChars); i++ {
			cr.allowed[rule.AllowedChars[i]] = true
		}
		c.safeHeaders[nn] = cr
	}
	for _, h := range cfg.SafeResponseHeaders {
		nn, ok := normalizeHeaderName(h)
		if !ok {
			return nil, invalidArg("invalid safe response header name %q", h)
		}
		c.safeResponses[nn] = struct{}{}
	}
	return c, nil
}

// isTokenByte 报告 b 是否为合法头名字字符（RFC 7230 token）。
func isTokenByte(b byte) bool {
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

// normalizeHeaderName 把头名字规范化为小写；含非法字符或为空时返回 false。
func normalizeHeaderName(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	b := []byte(name)
	for i := 0; i < len(b); i++ {
		if !isTokenByte(b[i]) {
			return "", false
		}
		if 'A' <= b[i] && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b), true
}

// isSimple 判定请求是否为简单请求：方法安全且每个头都安全且值满足约束。
func (c *classifier) isSimple(req Request) bool {
	if _, ok := c.safeMethods[req.Method]; !ok {
		return false
	}
	for _, h := range req.Headers {
		nn, ok := normalizeHeaderName(h.Name)
		if !ok {
			return false
		}
		rule, ok := c.safeHeaders[nn]
		if !ok {
			return false
		}
		if len(h.Value) > rule.maxLength {
			return false
		}
		for i := 0; i < len(h.Value); i++ {
			if !rule.allowed[h.Value[i]] {
				return false
			}
		}
	}
	return true
}

// nonSafeHeaders 返回请求中名字不在安全头集合里的头（规范化、去重、排序）。
func (c *classifier) nonSafeHeaders(req Request) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, h := range req.Headers {
		nn, ok := normalizeHeaderName(h.Name)
		if !ok {
			continue
		}
		if _, safe := c.safeHeaders[nn]; safe {
			continue
		}
		if _, dup := seen[nn]; dup {
			continue
		}
		seen[nn] = struct{}{}
		out = append(out, nn)
	}
	sort.Strings(out)
	return out
}
