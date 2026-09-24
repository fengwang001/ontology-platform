package props

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"ontology/logical"
)

var ErrBadUnicode = errors.New("invalid unicode escape")

type UnicodeError struct {
	Line   int
	Column int
	Kind   string
}

func (e *UnicodeError) Error() string {
	return fmt.Sprintf("%s at line %d, column %d", ErrBadUnicode, e.Line, e.Column)
}

func (e *UnicodeError) Unwrap() error {
	return ErrBadUnicode
}

type Properties struct {
	order []string
	items map[string]string
}

func New(pairs []Pair) *Properties {
	properties := &Properties{order: make([]string, 0, len(pairs)), items: make(map[string]string)}
	for _, pair := range pairs {
		if _, exists := properties.items[pair.Key]; !exists {
			properties.order = append(properties.order, pair.Key)
		}
		properties.items[pair.Key] = pair.Value
	}
	return properties
}

func Load(input string) (*Properties, error) {
	properties := &Properties{items: make(map[string]string)}
	for _, line := range logical.NewScanner(input).Scan() {
		if line.Kind != logical.Data {
			continue
		}
		pair, err := parseLine(line.Fragments)
		if err != nil {
			return nil, err
		}
		if _, exists := properties.items[pair.Key]; !exists {
			properties.order = append(properties.order, pair.Key)
		}
		properties.items[pair.Key] = pair.Value
	}
	return properties, nil
}

func (p *Properties) Get(key string) (string, bool) {
	value, ok := p.items[key]
	return value, ok
}

func (p *Properties) All() []Pair {
	pairs := make([]Pair, 0, len(p.order))
	for _, key := range p.order {
		pairs = append(pairs, Pair{Key: key, Value: p.items[key]})
	}
	return pairs
}

type Pair struct {
	Key   string
	Value string
}

func (p *Properties) Store() string {
	var builder strings.Builder
	for _, key := range p.order {
		escapeKey(&builder, key)
		builder.WriteByte('=')
		escapeValue(&builder, p.items[key])
		builder.WriteByte('\n')
	}
	return builder.String()
}

type locator struct {
	fragments []logical.Fragment
	starts    []int
	text      string
}

func parseLine(fragments []logical.Fragment) (Pair, error) {
	var combined strings.Builder
	starts := make([]int, len(fragments))
	for index, fragment := range fragments {
		starts[index] = combined.Len()
		combined.WriteString(fragment.Text)
	}
	where := locator{fragments: fragments, starts: starts, text: combined.String()}
	offset := skipSpace(where.text, 0)
	key, valueOffset, err := readKey(&where, offset)
	if err != nil {
		return Pair{}, err
	}
	value, _, err := readValue(&where, valueOffset)
	if err != nil {
		return Pair{}, err
	}
	return Pair{Key: key, Value: value}, nil
}

func readKey(where *locator, offset int) (string, int, error) {
	var result strings.Builder
	for offset < len(where.text) {
		char := where.text[offset]
		switch {
		case char == '=' || char == ':':
			return result.String(), offset + 1, nil
		case isSpace(char):
			offset = skipSpace(where.text, offset)
			if offset < len(where.text) && (where.text[offset] == '=' || where.text[offset] == ':') {
				offset++
			}
			return result.String(), offset, nil
		case char == '\\':
			decoded, next, err := readEscape(where, offset)
			if err != nil {
				return "", 0, err
			}
			result.WriteString(decoded)
			offset = next
		default:
			_, size := utf8.DecodeRuneInString(where.text[offset:])
			result.WriteString(where.text[offset : offset+size])
			offset += size
		}
	}
	return result.String(), offset, nil
}

func readValue(where *locator, offset int) (string, int, error) {
	offset = skipSpace(where.text, offset)
	var result strings.Builder
	for offset < len(where.text) {
		if where.text[offset] == '\\' {
			decoded, next, err := readEscape(where, offset)
			if err != nil {
				return "", 0, err
			}
			result.WriteString(decoded)
			offset = next
			continue
		}
		_, size := utf8.DecodeRuneInString(where.text[offset:])
		result.WriteString(where.text[offset : offset+size])
		offset += size
	}
	return result.String(), offset, nil
}

func readEscape(where *locator, slash int) (string, int, error) {
	if slash+1 >= len(where.text) {
		return "", slash, badUnicode(where, slash)
	}
	char := where.text[slash+1]
	if char == 'u' {
		if slash+5 >= len(where.text) || !isHex(where.text[slash+2:slash+6]) {
			return "", slash, badUnicode(where, slash)
		}
		value := parseHex(where.text[slash+2 : slash+6])
		return string(rune(value)), slash + 6, nil
	}
	decoded := map[byte]byte{'t': '\t', 'n': '\n', 'r': '\r', 'f': '\f'}[char]
	if decoded == 0 {
		_, size := utf8.DecodeRuneInString(where.text[slash+1:])
		return where.text[slash+1 : slash+1+size], slash + 1 + size, nil
	}
	return string(decoded), slash + 2, nil
}

func (l *locator) locate(offset int) (int, int) {
	index := 0
	for index+1 < len(l.fragments) && l.starts[index+1] <= offset {
		index++
	}
	local := offset - l.starts[index]
	column := l.fragments[index].Column + utf8.RuneCountInString(l.fragments[index].Text[:local])
	return l.fragments[index].Physical, column
}

func badUnicode(where *locator, offset int) error {
	line, column := where.locate(offset)
	return &UnicodeError{Line: line, Column: column}
}

func skipSpace(text string, offset int) int {
	for offset < len(text) && isSpace(text[offset]) {
		offset++
	}
	return offset
}

func isSpace(char byte) bool {
	return char == ' ' || char == '\t' || char == '\f'
}

func isHex(text string) bool {
	for _, char := range text {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}

func parseHex(text string) int {
	value := 0
	for _, char := range text {
		value <<= 4
		switch {
		case char >= '0' && char <= '9':
			value += int(char - '0')
		case char >= 'a' && char <= 'f':
			value += int(char-'a') + 10
		default:
			value += int(char-'A') + 10
		}
	}
	return value
}

func escapeKey(builder *strings.Builder, key string) {
	for index, char := range key {
		switch char {
		case '\\', '=', ':', ' ', '\t', '\f', '\n', '\r':
			writeEscape(builder, char)
		case '#', '!':
			if index == 0 {
				builder.WriteByte('\\')
			}
			builder.WriteRune(char)
		default:
			builder.WriteRune(char)
		}
	}
}

func escapeValue(builder *strings.Builder, value string) {
	for index, char := range value {
		switch {
		case char == '\\' || char == '\n' || char == '\r':
			writeEscape(builder, char)
		case index == 0 && (char == ' ' || char == '\t' || char == '\f'):
			writeEscape(builder, char)
		default:
			builder.WriteRune(char)
		}
	}
}

func writeEscape(builder *strings.Builder, char rune) {
	switch char {
	case '\\':
		builder.WriteString(`\\`)
	case '\n':
		builder.WriteString(`\n`)
	case '\r':
		builder.WriteString(`\r`)
	case '\t':
		builder.WriteString(`\t`)
	case '\f':
		builder.WriteString(`\f`)
	default:
		builder.WriteByte('\\')
		builder.WriteRune(char)
	}
}
