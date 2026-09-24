package props

import (
	"fmt"
	"io"

	"ontology/logical"
)

type EscapeError struct {
	Line int
	Col  int
	Text string
}

func (e *EscapeError) Error() string {
	return fmt.Sprintf("%s at line %d, column %d", e.Text, e.Line, e.Col)
}

var errInvalidUnicode = "properties: invalid unicode escape"

type Properties struct {
	order []string
	value map[string]string
}

func Load(r io.Reader) (*Properties, error) {
	scanner, err := logical.NewScanner(r)
	if err != nil {
		return nil, err
	}
	props := New()
	for {
		line, ok := scanner.Next()
		if !ok {
			break
		}
		if line.Comment {
			continue
		}
		key, value, err := parseLine(line)
		if err != nil {
			return nil, err
		}
		props.Set(key, value)
	}
	return props, nil
}

func New() *Properties {
	return &Properties{order: []string{}, value: map[string]string{}}
}

func (p *Properties) Set(key, value string) {
	if _, exists := p.value[key]; !exists {
		p.order = append(p.order, key)
	}
	p.value[key] = value
}

func (p *Properties) Get(key string) (string, bool) {
	value, ok := p.value[key]
	return value, ok
}

func (p *Properties) Len() int { return len(p.order) }

func (p *Properties) All() []Pair {
	pairs := make([]Pair, 0, len(p.order))
	for _, key := range p.order {
		pairs = append(pairs, Pair{Key: key, Value: p.value[key]})
	}
	return pairs
}

type Pair struct {
	Key   string
	Value string
}

func (p *Properties) Store(w io.Writer) error {
	var builder []byte
	for _, key := range p.order {
		builder = appendEscaped(builder, key, true, 0)
		builder = append(builder, '=')
		builder = appendEscaped(builder, p.value[key], false, 0)
		builder = append(builder, '\n')
	}
	_, err := w.Write(builder)
	return err
}

func parseLine(line *logical.Line) (string, string, error) {
	var key, value []byte
	var partIndex, offset, state int
	for partIndex < len(line.Parts) {
		part := line.Parts[partIndex]
		if offset < len(part.Data) {
			b := part.Data[offset]
			switch {
			case state == 0 && isSpace(b):
			case state <= 1 && (b == '=' || b == ':'):
				state = 1
			case state == 1 && isSpace(b):
				state = 2
			case state == 1 && b == '\\':
				decoded, next, err := decodeEscape(part, offset)
				if err != nil {
					return "", "", err
				}
				key, offset = appendDecoded(key, decoded, part, next, &partIndex), next
				continue
			case state == 1:
				key = append(key, b)
			case state == 0 && b == '\\':
				decoded, next, err := decodeEscape(part, offset)
				if err != nil {
					return "", "", err
				}
				key, offset = appendDecoded(key, decoded, part, next, &partIndex), next
				state = 1
				continue
			case state == 2 && isSpace(b):
			case state == 2 && (b == '=' || b == ':'):
			case state == 2:
				state = 3
				value = append(value, b)
			case state == 3 && b == '\\':
				decoded, next, err := decodeEscape(part, offset)
				if err != nil {
					return "", "", err
				}
				value, offset = appendDecoded(value, decoded, part, next, &partIndex), next
				continue
			case state == 0:
				key, state = append(key, b), 1
			default:
				value = append(value, b)
			}
		}
		partIndex, offset = partIndex+1, 0
	}
	return string(key), string(value), nil
}

func decodeEscape(part logical.Part, offset int) (rune, int, error) {
	next := offset + 1
	if next >= len(part.Data) {
		return 0, next, nil
	}
	switch part.Data[next] {
	case 't':
		return '\t', next + 1, nil
	case 'n':
		return '\n', next + 1, nil
	case 'r':
		return '\r', next + 1, nil
	case 'f':
		return '\f', next + 1, nil
	case 'u':
		if next+4 >= len(part.Data) {
			return 0, next, &EscapeError{part.Line, part.Col + offset, errInvalidUnicode}
		}
		var decoded rune
		for _, b := range part.Data[next+1 : next+5] {
			n, ok := hexValue(b)
			if !ok {
				return 0, next, &EscapeError{part.Line, part.Col + offset, errInvalidUnicode}
			}
			decoded = decoded*16 + rune(n)
		}
		return decoded, next + 5, nil
	default:
		return rune(part.Data[next]), next + 1, nil
	}
}

func appendDecoded(dst []byte, decoded rune, part logical.Part, next int, index *int) []byte {
	if decoded != 0 {
		dst = appendRune(dst, decoded)
	}
	if next > len(part.Data) {
		*index++
	}
	return dst
}

func appendRune(dst []byte, decoded rune) []byte {
	var buffer [4]byte
	return append(dst, buffer[:utf8EncodeRune(buffer[:], decoded)]...)
}

func appendEscaped(dst []byte, text string, key bool, start int) []byte {
	for i, r := range text {
		switch {
		case r == '\\':
			dst = append(dst, '\\', '\\')
		case r == '\n':
			dst = append(dst, '\\', 'n')
		case r == '\r':
			dst = append(dst, '\\', 'r')
		case r == '\t' && (key || i == start):
			dst = append(dst, '\\', 't')
		case r == '\f' && (key || i == start):
			dst = append(dst, '\\', 'f')
		case r == ' ' && (key || i == start):
			dst = append(dst, '\\', ' ')
		case key && (r == '=' || r == ':'):
			dst = append(dst, '\\', byte(r))
		case key && i == start && (r == '#' || r == '!'):
			dst = append(dst, '\\', byte(r))
		default:
			dst = appendRune(dst, r)
		}
	}
	return dst
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\f' }

func hexValue(b byte) (int, bool) {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0'), true
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10, true
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10, true
	default:
		return 0, false
	}
}

func utf8EncodeRune(dst []byte, r rune) int {
	switch {
	case r < 0x80:
		dst[0] = byte(r)
		return 1
	case r < 0x800:
		dst[0] = byte(0xc0 | r>>6)
		dst[1] = byte(0x80 | r&0x3f)
		return 2
	case r < 0x10000:
		dst[0] = byte(0xe0 | r>>12)
		dst[1] = byte(0x80 | (r>>6)&0x3f)
		dst[2] = byte(0x80 | r&0x3f)
		return 3
	default:
		dst[0] = byte(0xf0 | r>>18)
		dst[1] = byte(0x80 | (r>>12)&0x3f)
		dst[2] = byte(0x80 | (r>>6)&0x3f)
		dst[3] = byte(0x80 | r&0x3f)
		return 4
	}
}
