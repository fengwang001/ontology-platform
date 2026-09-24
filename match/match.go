// Package match implements cookie domain and path matching rules.
package match

import "strings"

// DefaultPath derives the default cookie path from the origin URL path:
// the part before the last '/', or "/" when that is empty or the path
// does not start with '/'.
func DefaultPath(p string) string {
	if !strings.HasPrefix(p, "/") {
		return "/"
	}
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return "/"
}

// PathMatch reports whether reqPath is covered by cookiePath.
// It is not a plain prefix match: "/a" matches "/a", "/a/", "/a/b"
// but not "/ab".
func PathMatch(cookiePath, reqPath string) bool {
	if reqPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(reqPath, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") || reqPath[len(cookiePath)] == '/'
}

// DomainMatch reports whether host is domain or a subdomain of it.
func DomainMatch(domain, host string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// DomainAllowed reports whether host may set a cookie for domain:
// host must be domain or its subdomain, and single-label domains
// (public-suffix level, e.g. "com") are rejected.
func DomainAllowed(host, domain string) bool {
	return strings.Contains(domain, ".") && DomainMatch(domain, host)
}
