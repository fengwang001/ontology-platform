package ontology

// RawValue is a property's original (pre-mask) value.
type RawValue = any

// DataType identifies a declared property type.
type DataType int

const (
	TypeInt DataType = iota + 1
	TypeFloat
	TypeString
	TypeBool
)

func (t DataType) String() string {
	switch t {
	case TypeInt:
		return "int"
	case TypeFloat:
		return "float"
	case TypeString:
		return "string"
	case TypeBool:
		return "bool"
	default:
		return "unknown"
	}
}

// ZeroValue returns the canonical zero value of a declared type.
// It is distinct from absence: a present zero value is stored as
// present and is observable as present.
func (t DataType) ZeroValue() RawValue {
	switch t {
	case TypeInt:
		return int64(0)
	case TypeFloat:
		return float64(0)
	case TypeString:
		return ""
	case TypeBool:
		return false
	default:
		return nil
	}
}

// CheckValue reports whether v satisfies the declared type's value
// contract (dynamic kind plus type-specific constraints). Mask-derived
// values are checked with the exact same contract, so a masking
// function returning a wrong-kind or out-of-contract value is a
// read-time masking type conflict rather than a leaked bad view.
func (t DataType) CheckValue(v RawValue) bool {
	switch t {
	case TypeInt:
		_, ok := v.(int64)
		return ok
	case TypeFloat:
		f, ok := v.(float64)
		return ok && f == f // reject NaN, which violates the value contract
	case TypeString:
		_, ok := v.(string)
		return ok
	case TypeBool:
		_, ok := v.(bool)
		return ok
	default:
		return false
	}
}

// PropertyDecl is one property declaration. Duplicate names within an
// ObjectType are rejected at construction time.
type PropertyDecl struct {
	Name string
	Type DataType
}

// ObjectType declares an ordered set of properties. Property order is
// fixed at construction and is the stable iteration order used by all
// result views, so engine output never depends on map iteration.
type ObjectType struct {
	Name   string
	props  []PropertyDecl
	index  map[string]int
	types_ map[string]DataType
}

// NewObjectType validates and registers a type declaration.
func NewObjectType(name string, props []PropertyDecl) (*ObjectType, error) {
	ot := &ObjectType{
		Name:   name,
		index:  map[string]int{},
		types_: map[string]DataType{},
	}
	for i, p := range props {
		if p.Name == "" {
			return nil, &DecisionError{ErrInvalidType, "property name must not be empty"}
		}
		if p.Type < TypeInt || p.Type > TypeBool {
			return nil, &DecisionError{ErrInvalidType, "property " + p.Name + " has unknown type"}
		}
		if _, dup := ot.index[p.Name]; dup {
			return nil, &DecisionError{ErrInvalidType, "duplicate property " + p.Name}
		}
		ot.index[p.Name] = i
		ot.types_[p.Name] = p.Type
		ot.props = append(ot.props, p)
	}
	return ot, nil
}

// Properties returns the declared properties in declaration order.
func (t *ObjectType) Properties() []PropertyDecl { return t.props }

// Has reports whether name is a declared property.
func (t *ObjectType) Has(name string) bool {
	_, ok := t.index[name]
	return ok
}

// PropertyType returns the declared type and presence of name.
func (t *ObjectType) PropertyType(name string) (DataType, bool) {
	dt, ok := t.types_[name]
	return dt, ok
}

// rawCell is the internal representation of one property slot. The
// three internal states are uniquely encoded:
//   - cell absent            -> property declared, no raw value (raw NULL)
//   - cell present, v==zero  -> property declared, raw value is the zero
//   - no cell at all         -> property not declared on the type
type rawCell struct {
	present bool
	value   RawValue
}

// Instance is the raw stored state of one object. Only raw values live
// here; masking is never written back into storage.
type Instance struct {
	ID          string
	cells       map[string]rawCell
	Version     int64
	LastWriteID int64
}

// NewInstance creates an instance. Values for every declared property
// may be supplied via present; properties omitted from present are
// stored as genuinely absent (raw NULL), never silently zero-filled.
// Values for undeclared properties and values violating the declared
// type contract are rejected.
func (t *ObjectType) NewInstance(id string, present map[string]RawValue) (*Instance, error) {
	inst := &Instance{ID: id, cells: map[string]rawCell{}}
	for name, v := range present {
		dt, ok := t.types_[name]
		if !ok {
			return nil, &DecisionError{ErrUnknownProperty, "unknown property " + name}
		}
		if !dt.CheckValue(v) {
			return nil, &DecisionError{ErrInvalidValueType, "value for " + name + " violates declared type " + dt.String()}
		}
		inst.cells[name] = rawCell{present: true, value: v}
	}
	return inst, nil
}

func (inst *Instance) clone() *Instance {
	cp := &Instance{ID: inst.ID, cells: make(map[string]rawCell, len(inst.cells)), Version: inst.Version, LastWriteID: inst.LastWriteID}
	for k, c := range inst.cells {
		cp.cells[k] = c
	}
	return cp
}

// Subject is the acting principal. Policy matching uses the subject id
// and group membership only; raw attributes do not participate.
type Subject struct {
	ID     string
	Groups []string
}

// SubjectSelector matches when MatchAll is set, or the subject id is in
// Users, or the subject belongs to at least one listed group.
type SubjectSelector struct {
	MatchAll bool
	Users    []string
	Groups   []string
}

func (s SubjectSelector) matches(sub Subject) bool {
	if s.MatchAll {
		return true
	}
	for _, u := range s.Users {
		if u == sub.ID {
			return true
		}
	}
	for _, g := range s.Groups {
		for _, sg := range sub.Groups {
			if g == sg {
				return true
			}
		}
	}
	return false
}
