package paramjoin

import (
	"sort"
	"strings"

	"ontology/paramlex"
)

type Param struct {
	Name     string
	Extended bool
	Value    string
}

func Decode(header string) (string, []Param, error) {
	disposition, raw, err := paramlex.Parse(header)
	if err != nil {
		return "", nil, err
	}
	params, err := Join(raw)
	return disposition, params, err
}

func Join(raw []paramlex.RawParam) ([]Param, error) {
	type groupKey struct {
		name string
		ext  bool
	}
	segments := map[groupKey]map[int]paramlex.RawParam{}
	for _, item := range raw {
		extended := item.Segment == 0 && item.Extended
		if item.Segment > 0 {
			if _, ok := segments[groupKey{item.Name, true}]; ok {
				extended = true
			}
		}
		key := groupKey{item.Name, extended}
		if segments[key] == nil {
			segments[key] = map[int]paramlex.RawParam{}
		}
		if _, ok := segments[key][item.Segment]; ok {
			return nil, ErrSyntax
		}
		segments[key][item.Segment] = item
	}

	keys := make([]groupKey, 0, len(segments))
	for key := range segments {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].name != keys[j].name {
			return keys[i].name < keys[j].name
		}
		return keys[i].ext
	})

	result := make([]Param, 0, len(keys))
	for _, key := range keys {
		parts := segments[key]
		for want := 0; ; want++ {
			item, ok := parts[want]
			if !ok {
				if want == 0 && len(parts) > 0 {
					return nil, IncompleteError{Missing: 0}
				}
				if len(parts) == want {
					break
				}
				return nil, IncompleteError{Missing: want}
			}
			if item.Extended && want == 0 {
				decoded, err := decodeFirst(item.Value)
				if err != nil {
					return nil, err
				}
				item.Value = decoded
				parts[want] = item
				continue
			}
		}
		var b strings.Builder
		for i := 0; i < len(parts); i++ {
			b.WriteString(parts[i].Value)
		}
		result = append(result, Param{Name: key.name, Extended: key.ext, Value: b.String()})
	}
	return result, nil
}
