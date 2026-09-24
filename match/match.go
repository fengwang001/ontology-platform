// Package match 提供域匹配与路径匹配的判定规则。
package match

import "strings"

// DefaultPath 由来源 URL 路径推导 cookie 的默认路径：
// 取最后一个 "/" 之前的部分；来源路径不以 "/" 开头或推导结果为空时为 "/"。
func DefaultPath(urlPath string) string {
	if !strings.HasPrefix(urlPath, "/") {
		return "/"
	}
	dir := urlPath[:strings.LastIndexByte(urlPath, '/')]
	if dir == "" {
		return "/"
	}
	return dir
}

// PathMatch 判定 cookie 路径是否适用于请求路径（非朴素前缀）。
func PathMatch(cookiePath, reqPath string) bool {
	if cookiePath == reqPath {
		return true
	}
	if !strings.HasPrefix(reqPath, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") || reqPath[len(cookiePath)] == '/'
}

// DomainMatch 判定请求主机是否命中 cookie 域。
// hostOnly 为真时仅匹配主机自身；否则匹配该域及其子域。
func DomainMatch(cookieDomain, host string, hostOnly bool) bool {
	host = strings.ToLower(host)
	if hostOnly {
		return host == cookieDomain
	}
	return host == cookieDomain || strings.HasSuffix(host, "."+cookieDomain)
}

// DomainAllowed 判定来源主机 host 是否被允许设置 Domain=domain：
// domain 须等于 host 或为 host 的父域，且不得为单标签（公共后缀级）域。
func DomainAllowed(host, domain string) bool {
	host = strings.ToLower(host)
	if !strings.Contains(domain, ".") {
		return false
	}
	return host == domain || strings.HasSuffix(host, "."+domain)
}
