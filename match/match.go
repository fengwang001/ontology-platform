// Package match 提供域匹配与路径匹配的判定规则，不依赖其他包。
package match

import "strings"

// Domain 判定请求主机 host 是否匹配 cookie 域 d。
// d 须已去掉前导点并小写化；host 等于 d 或为 d 的子域时匹配。
func Domain(host, d string) bool {
	host = strings.ToLower(host)
	return host == d || strings.HasSuffix(host, "."+d)
}

// Path 判定请求路径 req 是否匹配 cookie 路径 p。
// 不是朴素前缀匹配：/a 匹配 /a、/a/、/a/b，不匹配 /ab。
func Path(req, p string) bool {
	if req == p {
		return true
	}
	if !strings.HasPrefix(req, p) {
		return false
	}
	return strings.HasSuffix(p, "/") || req[len(p)] == '/'
}

// DefaultPath 求 cookie 的有效路径：path 非空则原样返回，
// 否则由来源 URL 路径推导（DESIGN.md 第 1 节）：
// 取最后一个 "/" 之前的部分；结果为空或路径不以 "/" 开头则为 "/"。
func DefaultPath(path, urlPath string) string {
	if path != "" {
		return path
	}
	if !strings.HasPrefix(urlPath, "/") {
		return "/"
	}
	if i := strings.LastIndex(urlPath, "/"); i > 0 {
		return urlPath[:i]
	}
	return "/"
}
