package headers

import "strings"

type pendingField struct {
	name  string
	value string
}

func Parse(raw string, single []string) (*Set, error) {
	raw = strings.Clone(raw)
	if raw != "" && !strings.HasSuffix(raw, "\r\n") {
		return nil, ErrMalformed
	}

	singleSet := make(map[string]bool, len(single))
	for _, n := range single {
		singleSet[canonical(n)] = true
	}

	set := &Set{index: make(map[string]int)}
	var pending *pendingField

	flush := func() error {
		if pending == nil {
			return nil
		}
		value := trimValue(pending.value)
		key := canonical(pending.name)
		if i, ok := set.index[key]; ok {
			if singleSet[key] {
				if set.entries[i].values[0] != value {
					return ErrSingleValue
				}
				return nil
			}
			set.entries[i].values = append(set.entries[i].values, value)
		} else {
			set.index[key] = len(set.entries)
			set.entries = append(set.entries, entry{name: key, values: []string{value}})
		}
		pending = nil
		return nil
	}

	if raw == "" {
		return set, nil
	}
	body := strings.TrimSuffix(raw, "\r\n")
	for _, line := range strings.Split(body, "\r\n") {
		if strings.ContainsAny(line, "\r\n") {
			return nil, ErrMalformed
		}
		if isFoldingLine(line) {
			if pending == nil {
				return nil, ErrMalformed
			}
			pending.value = appendFold(pending.value, line)
			continue
		}
		if err := flush(); err != nil {
			return nil, err
		}
		name, value, ok := splitFieldLine(line)
		if !ok {
			return nil, ErrMalformed
		}
		pending = &pendingField{name: name, value: value}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return set, nil
}

func splitFieldLine(line string) (name, value string, ok bool) {
	colon := strings.IndexByte(line, ':')
	if colon <= 0 {
		return "", "", false
	}
	name = line[:colon]
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == ' ' || c <= 0x1f || c == 0x7f {
			return "", "", false
		}
	}
	value = line[colon+1:]
	if strings.HasPrefix(value, " ") || strings.HasPrefix(value, "\t") {
		value = strings.TrimLeft(value, " \t")
	}
	return name, value, true
}
