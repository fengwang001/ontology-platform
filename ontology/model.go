package ontology

type Operation string

const (
	Read  Operation = "read"
	Write Operation = "write"
)

type WritePolicy string

const (
	RejectObject WritePolicy = "reject_object"
	IgnoreField  WritePolicy = "ignore_field"
)

type Property struct {
	LineageID  string
	Identifier string
	Required   bool
	Constraint string
}

type TypeVersion struct {
	Version    int
	Properties []Property
	byName     map[string]string
}

type CreateTypeInput struct {
	Name        string
	Properties  []Property
	WritePolicy WritePolicy
}

type CreateObjectInput struct {
	Type   string
	ID     string
	Fields map[string]any
	Actor  string
}

type ConfigureWritePolicyInput struct {
	Type         string
	RejectObject bool
	IgnoreField  bool
}

type Evolution struct {
	Type      string
	Add       []Property
	Rename    []Rename
	Deprecate []string
	Tighten   []Constraint
}

type Rename struct {
	From string
	To   string
}

type Constraint struct {
	Property   string
	Constraint string
}

type GrantInput struct {
	Type       string
	Subject    string
	PropertyID string
	Operation  Operation
}

type TypeGrantInput struct {
	Type      string
	Subject   string
	Operation Operation
}

type WriteInput struct {
	ObjectID      string
	SchemaVersion int
	Fields        map[string]any
	Actor         string
}

type WriteResult struct {
	SchemaVersion int
	Written       map[string]any
	Ignored       []string
}

type ReadResult struct {
	ObjectID      string
	Type          string
	SchemaVersion int
	PartialView   bool
	Object        map[string]any
	Removed       []string
}

type AttributePermissionView struct {
	LineageID    string
	ReadAllowed  bool
	ReadRevoked  bool
	WriteAllowed bool
	WriteRevoked bool
}

type PermissionView struct {
	Type                      string
	Subject                   string
	Version                   int
	TypeRead                  bool
	TypeWrite                 bool
	Attributes                map[string]AttributePermissionView
	AccessedVersionRecords    int
	AccessedPermissionRecords int
}

type AuditQuery struct {
	Type string
}
