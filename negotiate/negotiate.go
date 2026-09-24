package negotiate

import (
	"errors"
	"strings"

	"ontology/mtype"
	"ontology/rank"
)

var ErrNotAcceptable = errors.New("not acceptable")

type Result struct {
	Index int
	Type  mtype.MediaType
}

func Select(accept *string, candidates []string) (Result, error) {
	items, err := parseHeader(accept)
	if err != nil {
		return Result{}, err
	}
	best := Result{Index: -1}
	var bestScore rank.Score
	found := false
	for index, raw := range candidates {
		candidate, err := mtype.Parse(raw, index)
		if err != nil {
			return Result{}, err
		}
		var chosen rank.Score
		matched := false
		for _, item := range items {
			score, ok := rank.ScoreMatch(candidate, item)
			if !ok {
				continue
			}
			if !matched || score.Specificity > chosen.Specificity {
				chosen = score
				matched = true
			}
		}
		if !matched || chosen.Q == 0 {
			continue
		}
		if !found || chosen.Q > bestScore.Q ||
			(chosen.Q == bestScore.Q && chosen.Specificity > bestScore.Specificity) {
			best = Result{Index: index, Type: candidate}
			bestScore = chosen
			found = true
		}
	}
	if !found {
		return Result{}, ErrNotAcceptable
	}
	return best, nil
}

func parseHeader(accept *string) ([]rank.AcceptItem, error) {
	if accept == nil {
		item, _ := rank.ParseAcceptItem("*/*", 0)
		return []rank.AcceptItem{item}, nil
	}
	if strings.TrimSpace(*accept) == "" {
		return nil, ErrNotAcceptable
	}
	parts := splitHeader(*accept)
	items := make([]rank.AcceptItem, 0, len(parts))
	for index, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		item, err := rank.ParseAcceptItem(part, index)
		if err != nil {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

func splitHeader(header string) []string {
	var parts []string
	start := 0
	quoted := false
	for i := 0; i < len(header); i++ {
		switch header[i] {
		case '\\':
			if quoted && i+1 < len(header) {
				i++
			}
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				parts = append(parts, header[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, header[start:])
}
