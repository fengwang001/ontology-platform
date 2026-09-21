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
	return checkDangerousURLPrefix(nil, value)
}

// checkDangerousURLPrefix is checkDangerousURL evaluated against the
// concatenation of the URL attribute's already-rendered prefix and the next
// interpolated value. prefix must already be whitespace-stripped and
// lowercased; the scanner maintains it that way while feeding literal
// template text and interpolated values.
func checkDangerousURLPrefix(prefix []byte, value string) *DangerousURLError {
	head := make([]byte, 0, maxSchemeProbe)
	head = append(head, prefix...)
	for i := 0; i < len(value) && len(head) < maxSchemeProbe; i++ {
		ch := value[i]
		if isASCIISpace(ch) {
			continue
		}
		head = append(head, lowerASCII(ch))
	}
	for _, p := range dangerousURLPrefixes {
		if len(head) >= len(p) && string(head[:len(p)]) == p {
			return &DangerousURLError{Value: string(prefix) + value}
		}
	}
	return nil
}

// maxSchemeProbe is longer than any dangerous prefix, leaving room even though
// prefixes are compared directly.
const maxSchemeProbe = 32
