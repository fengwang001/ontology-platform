package ontology

import "fmt"

// ParamType is the declared type of an action parameter.
type ParamType string

const (
	TypeString ParamType = "string"
	TypeInt    ParamType = "int"
	TypeFloat  ParamType = "float"
	TypeBool   ParamType = "bool"
	TypeMap    ParamType = "map"
	TypeList   ParamType = "list"
	TypeAny    ParamType = "any"
)

// ParamSpec declares one parameter of an ActionType.
type ParamSpec struct {
	Name     string
	Type     ParamType
	Required bool
	Default  any // used when the parameter is optional and absent
}

// HookFunc is a pre-execution validation hook. A non-nil return rejects the
// action with the returned reason. Hooks may read but never write.
type HookFunc func(*HookCtx) error

// ActionFunc is the body of an action, executed inside the transaction.
type ActionFunc func(*Tx, map[string]any) error

// ActionType declares an action: its parameter contract, the object types it
// intends to touch, its pre-hooks and its body.
type ActionType struct {
	Name        string
	Params      []ParamSpec
	ObjectTypes []string
	Hooks       []HookFunc
	Run         ActionFunc
}

// Validate checks params against the schema and returns the normalized
// parameter map (defaults filled in with fresh deep copies). All problems
// are reported at once via *ParamErrors.
func (a *ActionType) Validate(params map[string]any) (map[string]any, error) {
	if params == nil {
		params = map[string]any{}
	}
	declared := make(map[string]ParamSpec, len(a.Params))
	for _, p := range a.Params {
		declared[p.Name] = p
	}
	errs := &ParamErrors{Action: a.Name}
	for name := range params {
		if _, ok := declared[name]; !ok {
			errs.Issues = append(errs.Issues, ParamIssue{Kind: IssueUnknown, Name: name})
		}
	}
	out := make(map[string]any, len(a.Params))
	for _, spec := range a.Params {
		v, ok := params[spec.Name]
		if !ok {
			if spec.Required {
				errs.Issues = append(errs.Issues, ParamIssue{Kind: IssueMissing, Name: spec.Name})
				continue
			}
			if spec.Default != nil {
				out[spec.Name] = deepCopy(spec.Default)
			}
			continue
		}
		if !typeMatches(spec.Type, v) {
			errs.Issues = append(errs.Issues, ParamIssue{
				Kind: IssueType, Name: spec.Name, Want: spec.Type, Got: typeName(v),
			})
			continue
		}
		out[spec.Name] = v
	}
	if len(errs.Issues) > 0 {
		return nil, errs
	}
	return out, nil
}

func typeMatches(t ParamType, v any) bool {
	switch t {
	case TypeAny:
		return true
	case TypeString:
		_, ok := v.(string)
		return ok
	case TypeBool:
		_, ok := v.(bool)
		return ok
	case TypeMap:
		_, ok := v.(map[string]any)
		return ok
	case TypeList:
		_, ok := v.([]any)
		return ok
	case TypeInt:
		return isInt(v)
	case TypeFloat:
		return isFloat(v) || isInt(v)
	}
	return false
}

func isInt(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}

func isFloat(v any) bool {
	switch v.(type) {
	case float32, float64:
		return true
	}
	return false
}

func typeName(v any) string {
	return fmt.Sprintf("%T", v)
}
