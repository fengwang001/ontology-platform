package negotiate

import "strings"

// rule is one parsed Accept item: a media range with a q value.
type rule struct {
	media  mediaType
	milliQ int // q scaled by 1000, in [0, 1000]
	raw    string
}

// specificity ranks exact matches above wildcards:
// 2 = exact type/subtype, 1 = type wildcard, 0 = full wildcard.
func specificity(m mediaType) int {
	if m.typ == "*" {
		return 0
	}
	if m.sub == "*" {
		return 1
	}
	return 2
}

// parseAccept parses an Accept header into rules.
// An empty or all-whitespace header means "*/*" with q=1.
func parseAccept(accept string) ([]rule, error) {
	if strings.TrimSpace(accept) == "" {
		return []rule{{
			media:  mediaType{typ: "*", sub: "*"},
			milliQ: 1000,
			raw:    "*/*",
		}}, nil
	}
	items := strings.Split(accept, ",")
	rules := make([]rule, 0, len(items))
	for _, item := range items {
		r, err := parseRule(item)
		if err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// parseRule parses one Accept item such as "text/html;level=1;q=0.5".
// Parameters before q participate in matching; anything after the q
// parameter (accept-ext) is ignored.
func parseRule(item string) (rule, error) {
	var r rule
	parts := strings.Split(item, ";")
	typ, sub, err := parseTypeSub(parts[0])
	if err != nil {
		return r, err
	}
	r.media.typ, r.media.sub = typ, sub
	r.milliQ = 1000
	r.raw = strings.TrimSpace(item)
	for _, p := range parts[1:] {
		name, value, err := parseParam(p)
		if err != nil {
			return rule{}, err
		}
		if name == "q" {
			milli, err := parseQ(value)
			if err != nil {
				return rule{}, err
			}
			r.milliQ = milli
			break // accept-ext after q is ignored
		}
		if r.media.params == nil {
			r.media.params = make(map[string]string)
		}
		r.media.params[name] = value
	}
	return r, nil
}
