package b64

const Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

type Kind int

const (
	KindInvalidChar Kind = iota + 1
	KindNonCanonical
	KindPadding
	KindLength
	KindNewline
)

type Error struct {
	Kind   Kind
	Offset int
}

func (e Error) Error() string { return "b64: invalid input" }

func (e Error) Is(target error) bool {
	err, ok := target.(Error)
	return ok && e.Kind == err.Kind && (err.Offset == 0 || e.Offset == err.Offset)
}

var (
	ErrInvalidChar  = Error{Kind: KindInvalidChar}
	ErrNonCanonical = Error{Kind: KindNonCanonical}
	ErrPadding      = Error{Kind: KindPadding}
	ErrLength       = Error{Kind: KindLength}
	ErrNewline      = Error{Kind: KindNewline}
)

var values [256]int

func init() {
	for i := range values {
		values[i] = -1
	}
	for i, c := range []byte(Alphabet) {
		values[c] = i
	}
}

func Value(c byte) int { return values[c] }

func Encode3(in []byte) []byte {
	out := make([]byte, 4)
	v := uint(in[0]) << 16
	if len(in) > 1 {
		v |= uint(in[1]) << 8
	}
	if len(in) > 2 {
		v |= uint(in[2])
	}
	out[0] = Alphabet[(v>>18)&63]
	out[1] = Alphabet[(v>>12)&63]
	out[2], out[3] = '=', '='
	if len(in) > 1 {
		out[2] = Alphabet[(v>>6)&63]
	}
	if len(in) > 2 {
		out[3] = Alphabet[v&63]
	}
	return out
}

func Decode4(group []byte) ([]byte, error) {
	if len(group) != 4 {
		return nil, Error{Kind: KindLength}
	}
	vals := [4]int{}
	for i, c := range group {
		if c == '=' {
			if i < 2 {
				return nil, Error{Kind: KindPadding, Offset: i}
			}
			vals[i] = 0
			continue
		}
		if (i == 3 && group[2] == '=') || (i > 1 && group[i-1] == '=') {
			return nil, Error{Kind: KindPadding, Offset: i}
		}
		v := Value(c)
		if v < 0 {
			return nil, Error{Kind: KindInvalidChar, Offset: i}
		}
		vals[i] = v
	}

	v := uint(vals[0])<<18 | uint(vals[1])<<12 | uint(vals[2])<<6 | uint(vals[3])
	out := []byte{byte(v >> 16), byte(v >> 8), byte(v)}
	switch {
	case group[2] == '=' && group[3] == '=':
		if vals[1]&15 != 0 {
			return nil, Error{Kind: KindNonCanonical, Offset: 1}
		}
		return out[:1], nil
	case group[3] == '=':
		if vals[2]&3 != 0 {
			return nil, Error{Kind: KindNonCanonical, Offset: 2}
		}
		return out[:2], nil
	default:
		return out, nil
	}
}
