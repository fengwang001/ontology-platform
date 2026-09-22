package sizeline

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
)

type Extension struct {
	Key   string
	Value string
}

var (
	errMalformedLine = errors.New("malformed size line")
	errEmptyKey      = errors.New("empty extension key")
)

func ExtensionLength(extensions []Extension) int {
	total := 0
	for _, extension := range extensions {
		total += 1 + len(extension.Key) + 1 + EncodedValueLength(extension.Value)
	}
	return total
}

func EncodedValueLength(value string) int {
	if needsQuote(value) {
		length := 2
		for _, r := range value {
			if r == '"' || r == '\\' {
				length++
			}
			length += len(string(r))
		}
		return length
	}
	return len(value)
}

func Encode(size int, extensions []Extension) []byte {
	var line bytes.Buffer
	line.WriteString(strconv.FormatInt(int64(size), 16))
	for _, extension := range extensions {
		line.WriteByte(';')
		line.WriteString(extension.Key)
		line.WriteByte('=')
		if needsQuote(extension.Value) {
			line.WriteByte('"')
			for _, r := range extension.Value {
				if r == '"' || r == '\\' {
					line.WriteByte('\\')
				}
				line.WriteRune(r)
			}
			line.WriteByte('"')
		} else {
			line.WriteString(extension.Value)
		}
	}
	line.WriteString("\r\n")
	return line.Bytes()
}

func Parse(line []byte) (int, []Extension, error) {
	if !bytes.HasSuffix(line, []byte("\r\n")) {
		return 0, nil, errMalformedLine
	}
	line = bytes.TrimSuffix(line, []byte("\r\n"))
	semicolon := bytes.IndexByte(line, ';')
	sizePart := line
	if semicolon >= 0 {
		sizePart = line[:semicolon]
	}
	size64, err := strconv.ParseInt(string(sizePart), 16, 64)
	if err != nil || size64 < 0 {
		return 0, nil, fmt.Errorf("%w: bad size", errMalformedLine)
	}
	extensions, err := parseExtensions(line[semicolon+1:])
	if err != nil {
		return 0, nil, err
	}
	return int(size64), extensions, nil
}

func parseExtensions(data []byte) ([]Extension, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var extensions []Extension
	for len(data) > 0 {
		equals := bytes.IndexByte(data, '=')
		if equals <= 0 {
			return nil, fmt.Errorf("%w: missing key", errMalformedLine)
		}
		key := string(data[:equals])
		if key == "" {
			return nil, errEmptyKey
		}
		data = data[equals+1:]
		value, remainder, err := parseValue(data)
		if err != nil {
			return nil, err
		}
		extensions = append(extensions, Extension{Key: key, Value: value})
		if len(remainder) == 0 {
			break
		}
		if remainder[0] != ';' {
			return nil, fmt.Errorf("%w: bad extension separator", errMalformedLine)
		}
		data = remainder[1:]
	}
	return extensions, nil
}

func parseValue(data []byte) (string, []byte, error) {
	if len(data) == 0 {
		return "", nil, nil
	}
	if data[0] != '"' {
		semicolon := bytes.IndexByte(data, ';')
		if semicolon < 0 {
			return string(data), nil, nil
		}
		return string(data[:semicolon]), data[semicolon:], nil
	}
	var value bytes.Buffer
	escaped := false
	for index := 1; index < len(data); index++ {
		char := data[index]
		if escaped {
			value.WriteByte(char)
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if char == '"' {
			remainder := data[index+1:]
			if len(remainder) > 0 && remainder[0] != ';' {
				return "", nil, fmt.Errorf("%w: bad quoted end", errMalformedLine)
			}
			return value.String(), remainder, nil
		}
		value.WriteByte(char)
	}
	return "", nil, fmt.Errorf("%w: unterminated quoted value", errMalformedLine)
}

func needsQuote(value string) bool {
	for _, r := range value {
		if r == ';' || r == '=' || r == '"' || r == '\\' || r == '\r' || r == '\n' {
			return true
		}
	}
	return false
}
