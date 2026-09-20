package negotiate

import (
	"strconv"
	"strings"
)

// rule 是 Accept 头中的一项协商规则。
type rule struct {
	raw       string
	mediaType string
	subType   string
	params    map[string]string
	q         float64
}

// parseRules 解析 Accept 头为规则列表；空或全空白视为 */*。
func parseRules(accept string) ([]rule, error) {
	var rules []rule
	for _, item := range strings.Split(accept, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		r, err := parseRule(item)
		if err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	if len(rules) == 0 {
		rules = append(rules, rule{raw: "*/*", mediaType: "*", subType: "*", q: 1})
	}
	return rules, nil
}

// parseRule 解析单条 Accept 项，如 text/html;level=1;q=0.5。
func parseRule(item string) (rule, error) {
	parts := strings.Split(item, ";")
	typ, sub, err := splitMediaType(parts[0])
	if err != nil {
		return rule{}, err
	}
	r := rule{raw: item, mediaType: typ, subType: sub, q: 1}
	seenQ := false
	for _, p := range parts[1:] {
		if seenQ {
			continue // q 之后的参数一律忽略
		}
		name, value, ok := strings.Cut(strings.TrimSpace(p), "=")
		if !ok || strings.TrimSpace(name) == "" {
			return rule{}, ErrMalformed
		}
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		if name == "q" {
			q, err := parseQ(value)
			if err != nil {
				return rule{}, err
			}
			r.q = q
			seenQ = true
			continue
		}
		if r.params == nil {
			r.params = make(map[string]string)
		}
		r.params[name] = value
	}
	return r, nil
}

// splitMediaType 拆分并校验 type/subtype。
func splitMediaType(s string) (string, string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	typ, sub, ok := strings.Cut(s, "/")
	if !ok || typ == "" || sub == "" || strings.Contains(sub, "/") {
		return "", "", ErrMalformed
	}
	if strings.ContainsAny(typ+sub, " \t") {
		return "", "", ErrMalformed
	}
	if typ == "*" && sub != "*" {
		return "", "", ErrMalformed
	}
	return typ, sub, nil
}

// parseQ 解析 q 值：0、1 或最多三位小数，范围 [0,1]。
func parseQ(s string) (float64, error) {
	if !validQ(s) {
		return 0, ErrMalformed
	}
	q, err := strconv.ParseFloat(s, 64)
	if err != nil || q < 0 || q > 1 {
		return 0, ErrMalformed
	}
	return q, nil
}

func validQ(s string) bool {
	if s == "" {
		return false
	}
	intPart, fracPart, hasDot := strings.Cut(s, ".")
	if intPart != "0" && intPart != "1" {
		return false
	}
	if !hasDot {
		return true
	}
	if len(fracPart) > 3 {
		return false
	}
	for _, c := range fracPart {
		if c < '0' || c > '9' {
			return false
		}
	}
	// 1.xxx 只允许 1.0/1.00/1.000
	if intPart == "1" {
		for _, c := range fracPart {
			if c != '0' {
				return false
			}
		}
	}
	return true
}
