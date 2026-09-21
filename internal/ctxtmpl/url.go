package ctxtmpl

import "strings"

// dangerousURLPrefixes are the schemes that must never appear at the start of
// a URL attribute value.
var dangerousURLPrefixes = []string{
	"javascript:",
	"vbscript:",
	"data:",
}

// checkDangerousURL rejects values that resolve to a script/data scheme after
// stripping ASCII whitespace. Whitespace is removed everywhere (not just from
// the front) so embedded tabs and newlines such as "java\tscript:" cannot
// bypass the check.
func checkDangerousURL(value string) *DangerousURLError {
	stripped := stripASCIIWhitespace(value)
	if stripped == "" {
		return nil
	}
	head := stripped
	if len(head) > maxSchemeProbe {
		head = head[:maxSchemeProbe]
	}
	head = lowerASCIIString(head)
	for _, prefix := range dangerousURLPrefixes {
		if len(stripped) >= len(prefix) && head[:len(prefix)] == prefix {
			return &DangerousURLError{Value: value}
		}
	}
	return nil
}

// maxSchemeProbe is longer than any dangerous prefix, leaving room even though
// prefixes are compared directly.
const maxSchemeProbe = 32

func stripASCIIWhitespace(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		if !isASCIISpace(s[i]) {
			b = append(b, s[i])
		}
	}
	return string(b)
}

func lowerASCIIString(s string) string {
	b := []byte(s)
	for i := range b {
		b[i] = lowerASCII(b[i])
	}
	return string(b)
}

// maxDangerousPrefixLen is the byte length of the longest scheme in
// dangerousURLPrefixes; bytes past it can no longer change the verdict.
var maxDangerousPrefixLen = func() int {
	m := 0
	for _, prefix := range dangerousURLPrefixes {
		if len(prefix) > m {
			m = len(prefix)
		}
	}
	return m
}()

// urlFold appends frag to acc, the running whitespace-stripped, ASCII-lowercased
// prefix of a URL attribute value. Stripping whitespace while folding (instead
// of only from the front) means tabs/newlines embedded inside a scheme split
// across pieces are normalized too. Bytes beyond the longest dangerous prefix
// cannot matter and are dropped.
func urlFold(acc []byte, frag string) []byte {
	for i := 0; i < len(frag); i++ {
		ch := frag[i]
		if isASCIISpace(ch) {
			continue
		}
		if len(acc) >= maxDangerousPrefixLen {
			break
		}
		acc = append(acc, lowerASCII(ch))
	}
	return acc
}

// urlPrefixVerdict examines the folded prefix accumulated so far. matched
// reports that a dangerous scheme already spans the prefix; possible reports
// that the prefix is still a leading fragment of a dangerous scheme, so more
// bytes (literal or interpolated) could complete one.
func urlPrefixVerdict(acc []byte) (matched, possible bool) {
	for _, prefix := range dangerousURLPrefixes {
		switch {
		case len(acc) >= len(prefix) && string(acc[:len(prefix)]) == prefix:
			return true, false
		case strings.HasPrefix(prefix, string(acc)):
			possible = true
		}
	}
	return false, possible
}
