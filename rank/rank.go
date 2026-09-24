package rank

import (
	"errors"
	"fmt"
	"strings"

	"ontology/mtype"
)

var ErrInvalidQ = errors.New("invalid q value")

type AcceptItem struct {
	Media mtype.MediaType
	Q     int
}

type Score struct {
	Q           int
	Specificity int
}

type ParseError struct {
	Index int
	Kind  error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("media type item %d: %v", e.Index, e.Kind)
}

func (e *ParseError) Unwrap() error { return e.Kind }

func ParseAcceptItem(s string, index int) (AcceptItem, error) {
	media, err := mtype.Parse(s, index)
	if err != nil {
		return AcceptItem{}, err
	}
	q := 1000
	if raw, ok := media.Params["q"]; ok {
		q, err = parseQ(raw)
		if err != nil {
			return AcceptItem{}, &ParseError{index, ErrInvalidQ}
		}
		delete(media.Params, "q")
	}
	return AcceptItem{Media: media, Q: q}, nil
}

func Match(candidate, accept mtype.MediaType) (Score, bool) {
	if !mtype.EqualParams(candidate.Params, accept.Params) {
		return Score{}, false
	}
	if accept.Type == "*" && accept.Subtype == "*" {
		return Score{Specificity: 0}, true
	}
	if accept.Type != candidate.Type {
		return Score{}, false
	}
	if accept.Subtype == "*" {
		return Score{Specificity: 1}, true
	}
	if accept.Subtype != candidate.Subtype {
		return Score{}, false
	}
	return Score{Specificity: 2}, true
}

func ScoreMatch(candidate mtype.MediaType, accept AcceptItem) (Score, bool) {
	score, ok := Match(candidate, accept.Media)
	if !ok {
		return Score{}, false
	}
	score.Q = accept.Q
	return score, true
}

func parseQ(raw string) (int, error) {
	dot := strings.IndexByte(raw, '.')
	whole, fraction := raw, ""
	if dot >= 0 {
		whole, fraction = raw[:dot], raw[dot+1:]
		if strings.ContainsRune(fraction, '.') || len(fraction) == 0 || len(fraction) > 3 {
			return 0, ErrInvalidQ
		}
		for _, char := range fraction {
			if char < '0' || char > '9' {
				return 0, ErrInvalidQ
			}
		}
	}
	if whole != "0" && whole != "1" {
		return 0, ErrInvalidQ
	}
	for len(fraction) < 3 {
		fraction += "0"
	}
	value := 0
	for _, char := range fraction {
		value = value*10 + int(char-'0')
	}
	if whole == "1" && value != 0 {
		return 0, ErrInvalidQ
	}
	if whole == "1" {
		return 1000, nil
	}
	return value, nil
}
