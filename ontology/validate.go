package ontology

import "sort"

// validateRecord performs the spec's fixed-order DFS validation.
func validateRecord(fields []Field, rec map[string]any) error {
	return checkGroup(fields, rec, "")
}

func checkGroup(fields []Field, m map[string]any, prefix string) error {
	unknown := unknownKeys(fields, m)
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fieldErr(ErrUnknownField, joinPath(prefix, unknown[0]))
	}
	for i := range fields {
		f := &fields[i]
		p := joinPath(prefix, f.Name)
		v, present := m[f.Name]
		if !present || v == nil {
			if f.Rep == Required {
				return fieldErr(ErrMissingRequired, p)
			}
			continue
		}
		if err := checkFieldValue(f, v, p); err != nil {
			return err
		}
	}
	return nil
}

func checkFieldValue(f *Field, v any, p string) error {
	if f.Rep == Repeated {
		arr, ok := v.([]any)
		if !ok {
			return fieldErr(ErrType, p)
		}
		for j, elem := range arr {
			if elem == nil {
				return fieldErr(ErrType, joinPath(p, itoa(j)))
			}
			if len(f.Children) == 0 {
				if _, ok := elem.(int64); !ok {
					return fieldErr(ErrType, joinPath(p, itoa(j)))
				}
			} else {
				gm, ok := elem.(map[string]any)
				if !ok {
					return fieldErr(ErrType, joinPath(p, itoa(j)))
				}
				if err := checkGroup(f.Children, gm, joinPath(p, itoa(j))); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if len(f.Children) == 0 {
		if _, ok := v.(int64); !ok {
			return fieldErr(ErrType, p)
		}
		return nil
	}
	gm, ok := v.(map[string]any)
	if !ok {
		return fieldErr(ErrType, p)
	}
	return checkGroup(f.Children, gm, p)
}

func unknownKeys(fields []Field, m map[string]any) []string {
	known := make(map[string]struct{}, len(fields))
	for i := range fields {
		known[fields[i].Name] = struct{}{}
	}
	var out []string
	for k := range m {
		if _, ok := known[k]; !ok {
			out = append(out, k)
		}
	}
	return out
}

func joinPath(a, b string) string {
	if a == "" {
		return b
	}
	return a + "." + b
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	n := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		buf[n] = '-'
	}
	return string(buf[n:])
}
