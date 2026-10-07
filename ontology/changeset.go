package ontology

// ChangeKind identifies a single configuration mutation.
type ChangeKind int

const (
	AddObjects ChangeKind = iota
	UpsertLinkChange
	DeleteLinkChange
	SetOverrideChange
	ClearOverrideChange
	ApplyGrantChange
	RevokeGrantChange
)

// Change is one atomic step inside a ChangeSet.
type Change struct {
	Kind     ChangeKind
	Names    []string
	Link     LinkType
	Object   string
	Override OverrideMode
	Grant    Grant
}
