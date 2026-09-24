// Package rank matches one candidate against one Accept item and scores
// specificity. Q values are scaled to integers 0..1000.
package rank

import "ontology/mtype"

// Item is one parsed Accept entry.
type Item struct {
	Media mtype.Type
	Q     int // 0..1000
}

// ParseAccept parses a full Accept header value into items.
func ParseAccept(header string) ([]Item, error) {
	parts := splitList(header)
	items := make([]Item, 0, len(parts))
	for i, p := range parts {
		it, err := ParseItem(p, i)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, nil
}

// splitList splits on commas that are not inside quoted strings.
func splitList(s string) []string {
	var out []string
	start := 0
	inQuote, esc := false, false
	for i := 0; i < len(s); i++ {
		switch {
		case esc:
			esc = false
		case inQuote && s[i] == '\\':
			esc = true
		case s[i] == '"':
			inQuote = !inQuote
		case s[i] == ',' && !inQuote:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// ParseItem parses one Accept entry like `text/plain;format=flowed;q=0.5`.
func ParseItem(s string, index int) (Item, error) {
	mt, err := mtype.Parse(s, index)
	if err != nil {
		return Item{}, err
	}
	q := 1000
	if raw, ok := mt.Params["q"]; ok {
		v, ok := parseQ(raw)
		if !ok {
			return Item{}, &mtype.Error{Kind: mtype.ErrBadQ, Index: index, Text: s}
		}
		q = v
		delete(mt.Params, "q")
	}
	return Item{Media: mt, Q: q}, nil
}

// parseQ accepts 0, 1, or 0/1 with up to three decimals (1.xxx must be 1.000).
func parseQ(s string) (int, bool) {
	if s == "0" {
		return 0, true
	}
	if s == "1" {
		return 1000, true
	}
	if len(s) < 3 || len(s) > 5 || s[1] != '.' {
		return 0, false
	}
	v := 0
	for i := 2; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		v = v*10 + int(s[i]-'0')
	}
	for i := len(s) - 2; i < 3; i++ {
		v *= 10
	}
	if s[0] == '0' {
		return v, true
	}
	return 1000, s[0] == '1' && v == 0
}

// Specificity ranks a media range: */* < type/* < type/subtype.
func Specificity(t mtype.Type) int {
	if t.Type == "*" {
		return 1
	}
	if t.Subtype == "*" {
		return 2
	}
	return 3
}

// Match reports whether item matches candidate, and the specificity.
// Param sets must be identical (q excluded); param values are case sensitive.
func Match(candidate mtype.Type, item Item) (int, bool) {
	m := item.Media
	if m.Type != "*" && m.Type != candidate.Type {
		return 0, false
	}
	if m.Subtype != "*" && m.Subtype != candidate.Subtype {
		return 0, false
	}
	if !mtype.ParamsEqual(m.Params, candidate.Params) {
		return 0, false
	}
	return Specificity(m), true
}
