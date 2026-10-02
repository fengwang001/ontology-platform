package ontology

import "strings"

const (
	maxLeaves = 16
	maxDepth  = 6
)

// leafPath is a leaf column with its precomputed path and levels.
type leafPath struct {
	fields []*Field
	names  []string
	path   string
	maxRep int
	maxDef int
}

func validateSchema(fields []Field) ([]leafPath, error) {
	var leaves []leafPath
	err := walkSchema(fields, nil, nil, 0, &leaves)
	if err != nil {
		return nil, err
	}
	if len(leaves) < 1 || len(leaves) > maxLeaves {
		return nil, ErrSchema
	}
	return leaves, nil
}

func walkSchema(fields []Field, parentFields []*Field, parentNames []string, depth int, leaves *[]leafPath) error {
	seen := make(map[string]struct{}, len(fields))
	for i := range fields {
		f := &fields[i]
		if f.Name == "" {
			return ErrSchema
		}
		if _, dup := seen[f.Name]; dup {
			return ErrSchema
		}
		seen[f.Name] = struct{}{}
		if f.Rep < Required || f.Rep > Repeated {
			return ErrSchema
		}
		curFields := make([]*Field, len(parentFields)+1)
		copy(curFields, parentFields)
		curFields[len(parentFields)] = f
		names := append(append([]string{}, parentNames...), f.Name)
		if len(f.Children) == 0 {
			lp := leafPath{fields: curFields, names: names, path: strings.Join(names, ".")}
			for _, nf := range curFields {
				if nf.Rep == Repeated {
					lp.maxRep++
				}
				if nf.Rep != Required {
					lp.maxDef++
				}
			}
			*leaves = append(*leaves, lp)
		} else {
			if len(curFields) >= maxDepth {
				return ErrSchema
			}
			if err := walkSchema(f.Children, curFields, names, depth+1, leaves); err != nil {
				return err
			}
		}
	}
	return nil
}

// depth counts the nesting depth (number of nodes) of a leaf path.
func (lp leafPath) depth() int { return len(lp.fields) }
