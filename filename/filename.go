package filename

import (
	"errors"
	"strings"
	"unicode"

	"ontology/paramjoin"
)

const Fallback = "_file"

type Source int

const (
	None Source = iota
	Extended
	Plain
)

type Choice struct {
	Name   string
	Source Source
}

func Resolve(params []paramjoin.Param) (Choice, error) {
	extended, plain := "", ""
	for _, param := range params {
		if param.Name != "filename" {
			continue
		}
		if param.Extended {
			extended = param.Value
		} else {
			plain = param.Value
		}
	}
	if name := Sanitize(extended); name != Fallback {
		return Choice{Name: name, Source: Extended}, nil
	}
	if name := Sanitize(plain); name != Fallback {
		return Choice{Name: name, Source: Plain}, nil
	}
	return Choice{Name: Fallback, Source: None}, nil
}

func Decode(header string) (Choice, error) {
	disposition, params, err := paramjoin.Decode(header)
	if err != nil {
		return Choice{}, err
	}
	if disposition == "" {
		return Choice{}, errors.New("empty disposition")
	}
	return Resolve(params)
}

func Sanitize(raw string) string {
	decoded := percentDecode(raw)
	name := strings.TrimFunc(string(decoded), unicode.IsSpace)
	if name == "" || name == "." || name == ".." {
		return Fallback
	}
	return encodeDangerous(name)
}
