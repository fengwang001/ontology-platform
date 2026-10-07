package quota

import "unicode"

// ResourceName 是配额硬上限中的资源名，只允许三种形式：
// pods、requests.X、limits.X。
type ResourceName string

// ResourcePods 是按个数记账的资源名。
const ResourcePods ResourceName = "pods"

const (
	requestsPrefix = "requests."
	limitsPrefix   = "limits."
)

type resourceKind int

const (
	kindPods resourceKind = iota
	kindRequests
	kindLimits
)

// parseResourceName 解析硬上限资源名，返回类别与裸资源名 X（pods 时为空）。
func parseResourceName(r ResourceName) (resourceKind, string, bool) {
	if r == ResourcePods {
		return kindPods, "", true
	}
	if rest, ok := cutPrefix(string(r), requestsPrefix); ok && isValidBareResource(rest) {
		return kindRequests, rest, true
	}
	if rest, ok := cutPrefix(string(r), limitsPrefix); ok && isValidBareResource(rest) {
		return kindLimits, rest, true
	}
	return 0, "", false
}

func cutPrefix(s, prefix string) (string, bool) {
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}

// isValidBareResource 校验裸资源名：非空且不含空白字符。
func isValidBareResource(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if unicode.IsSpace(c) {
			return false
		}
	}
	return true
}
