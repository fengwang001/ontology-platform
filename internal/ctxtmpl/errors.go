package ctxtmpl

// Sentinel errors returned by Render. Callers can use errors.Is to classify
// failures without parsing human-readable messages.
var (
	// ErrUnknownKey reports an interpolation whose key is absent from data.
	ErrUnknownKey = sentinelError("ctxtmpl: referenced key is missing from data")

	// ErrInterpolationInTagName reports an interpolation at a position where
	// only a tag/attribute name or structural markup is allowed.
	ErrInterpolationInTagName = sentinelError("ctxtmpl: interpolation in tag name position")

	// ErrUnclosedTag reports that a '<' tag is still open at end of template.
	ErrUnclosedTag = sentinelError("ctxtmpl: unclosed tag at end of template")

	// ErrUnclosedQuote reports an attribute value quote without a match.
	ErrUnclosedQuote = sentinelError("ctxtmpl: unclosed attribute value quote")

	// ErrUnclosedComment reports an HTML comment without a terminating -->.
	ErrUnclosedComment = sentinelError("ctxtmpl: unclosed HTML comment")

	// ErrInvalidInterpolation reports malformed {{ ... }} syntax.
	ErrInvalidInterpolation = sentinelError("ctxtmpl: invalid interpolation")

	// ErrDangerousURL reports a URL-attribute value resolving to a script
	// scheme such as javascript:.
	ErrDangerousURL = sentinelError("ctxtmpl: dangerous URL scheme")
)

// sentinelError is an error value with no per-instance state so that equality
// (errors.Is without Unwrap) works for classification.
type sentinelError string

func (e sentinelError) Error() string { return string(e) }

// SyntaxError carries the byte offset where template scanning failed. It wraps
// one of the sentinel errors above.
type SyntaxError struct {
	Offset int
	Err    error
}

func (e *SyntaxError) Error() string {
	if e == nil {
		return "ctxtmpl: nil syntax error"
	}
	name := ""
	if e.Err != nil {
		name = e.Err.Error()
	}
	return name + " (offset " + itoa(e.Offset) + ")"
}

func (e *SyntaxError) Unwrap() error { return e.Err }

// UnknownKeyError names the missing key. It wraps ErrUnknownKey.
type UnknownKeyError struct {
	Key string
}

func (e *UnknownKeyError) Error() string {
	return "ctxtmpl: unknown key " + quote(e.Key)
}

func (e *UnknownKeyError) Unwrap() error { return ErrUnknownKey }

// DangerousURLError wraps ErrDangerousURL and retains the offending value.
type DangerousURLError struct {
	Value string
}

func (e *DangerousURLError) Error() string {
	return "ctxtmpl: dangerous URL " + quote(e.Value)
}

func (e *DangerousURLError) Unwrap() error { return ErrDangerousURL }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func quote(s string) string {
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b = append(b, '\\', '"')
		case c >= 0x20 && c < 0x7f:
			b = append(b, c)
		default:
			b = append(b, '?')
		}
	}
	b = append(b, '"')
	return string(b)
}
