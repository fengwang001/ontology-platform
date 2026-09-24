// Package match 实现 cookie 的域匹配与路径匹配判定规则（RFC 6265 §5.1 风格）。
package match

import "strings"

// PathMatch 判定请求路径 req 是否命中 cookie 路径 cp。
// 不是朴素前缀匹配：/a 命中 /a、/a/、/a/b，但不命中 /ab。
func PathMatch(cp, req string) bool {
	if cp == req {
		return true
	}
	if strings.HasPrefix(req, cp) {
		return strings.HasSuffix(cp, "/") || req[len(cp)] == '/'
	}
	return false
}

// DomainMatch 判定主机 host 是否属于域 domain（自身或子域），均先转小写。
func DomainMatch(host, domain string) bool {
	host, domain = strings.ToLower(host), strings.ToLower(domain)
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// SetDomainOK 判定来源主机 host 是否允许设置 Domain=domain：
// domain 必须是 host 自身或其上级域，且不得是单标签域（如 com）。
func SetDomainOK(host, domain string) bool {
	domain = strings.TrimPrefix(strings.ToLower(domain), ".")
	return DomainMatch(host, domain) && strings.Contains(domain, ".")
}

// DefaultPath 由来源 URL 路径推导默认 cookie 路径：
// 取最后一个 / 之前的部分；不以 / 开头或结果为空时为 /。
func DefaultPath(urlPath string) string {
	if !strings.HasPrefix(urlPath, "/") || strings.Count(urlPath, "/") == 1 {
		return "/"
	}
	return urlPath[:strings.LastIndex(urlPath, "/")]
}
