package ctxtmpl

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
