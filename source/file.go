package source

import (
	"fmt"
	"strconv"
	"strings"
)

func ParseFile(data []byte) (Layer, error) {
	layer, err := parseFileAt(data, len(data))
	return layer, err
}

func parseFileAt(data []byte, byteLen int) (Layer, error) {
	if len(data) == 0 {
		return Layer{Level: File}, nil
	}
	text := string(data)
	lines := strings.Split(text, "\n")
	count, err := strconv.Atoi(lines[0])
	if err != nil || count < 0 {
		return Layer{}, &FileError{Line: 1, Msg: "bad line count header"}
	}
	layer := Layer{Level: File, Items: make([]Item, 0, count)}
	for i := 1; i <= count; i++ {
		if i >= len(lines) {
			return Layer{}, &TruncationError{
				Class: TruncLine, Line: i + 1, Byte: byteLen}
		}
		if lines[i] == "" && i == len(lines)-1 && text[len(text)-1] == '\n' {
			return Layer{}, &TruncationError{
				Class: TruncLine, Line: i + 1, Byte: byteLen}
		}
		if lines[i] == "" {
			return Layer{}, &FileError{Line: i + 1, Msg: "empty key"}
		}
		row := lines[i]
		eq := strings.IndexByte(row, '=')
		if eq < 0 {
			return Layer{}, &TruncationError{
				Class: TruncKey, Line: i + 1, Byte: byteLen}
		}
		key := strings.TrimSpace(row[:eq])
		rest := strings.TrimSpace(row[eq+1:])
		if eq == len(row)-1 || len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
			return Layer{}, &TruncationError{
				Class: TruncValue, Line: i + 1, Byte: byteLen}
		}
		if err := ValidateKey(key); err != nil {
			return Layer{}, &FileError{Line: i + 1, Msg: err.Error()}
		}
		layer.Items = append(layer.Items, Item{Key: key, Value: rest[1 : len(rest)-1]})
	}
	if len(lines) != count+1 {
		return Layer{}, &FileError{Line: count + 2, Msg: "unexpected extra line"}
	}
	if err := checkUnique(layer); err != nil {
		return Layer{}, err
	}
	return layer, nil
}

func ClassifyTruncation(full []byte, byteLen int) (TruncationClass, error) {
	if byteLen < 0 || byteLen > len(full) {
		return 0, fmt.Errorf("byte length out of range: %d", byteLen)
	}
	if byteLen == len(full) {
		if _, err := ParseFile(full); err != nil {
			return 0, err
		}
		return 0, nil
	}
	var trunc *TruncationError
	_, err := parseFileAt(full[:byteLen], byteLen)
	if asTruncation(err, &trunc) {
		return trunc.Class, err
	}
	return 0, err
}

func asTruncation(err error, target **TruncationError) bool {
	for err != nil {
		if te, ok := err.(*TruncationError); ok {
			*target = te
			return true
		}
		next, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = next.Unwrap()
	}
	return false
}
