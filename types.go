package incremental

import (
	"errors"
	"fmt"
	"strings"
)

type DeclID string

type EditType int

const (
	AddDeclaration EditType = iota
	SetSignature
	SetImplementation
	DeleteDeclaration
)

type Edit struct {
	Type                 EditType
	ID                   DeclID
	SignatureSource      string
	ImplementationSource string
}

type Declaration struct {
	ID                   DeclID
	Present              bool
	SignatureSource      string
	ImplementationSource string
}

type Signature struct {
	Value     string
	ErrorText string
}

func (s Signature) Equal(other Signature) bool {
	return s.Value == other.Value && s.ErrorText == other.ErrorText
}

func (s Signature) Exists() bool {
	return !strings.HasPrefix(s.Value, "\x00missing:")
}

var ErrNotFound = errors.New("declaration not found")
var ErrSchedulerBusy = errors.New("scheduler is dispatching a check")

type SignatureResult struct {
	Present bool
	Version int
	Signature
	Basis Basis
}

type ImplementationResult struct {
	Present bool
	Signature
	Basis Basis
}

type BasisEntry struct {
	Version int
	Present bool
	Signature
}

type Basis map[DeclID]BasisEntry

type Checker interface {
	CheckSignature(*CheckContext, Declaration) Signature
	CheckImplementation(*CheckContext, Declaration) Signature
}

type CheckContext struct {
	id     DeclID
	reads  map[DeclID]bool
	lookup func(DeclID) (SignatureResult, bool)
}

func (c *CheckContext) Signature(id DeclID) Signature {
	c.reads[id] = true
	result, ok := c.lookup(id)
	if !ok || !result.Present {
		return MissingSignature(id)
	}
	return result.Signature
}

type Stats struct {
	LastRecheckedDeclarations    int
	LastEarlyStoppedDeclarations int
	ReuseCount                   int64
}

type EditOutcome struct {
	Rechecked    []DeclID
	EarlyStopped []DeclID
	SigVersions  map[DeclID]int
	NoOp         bool
	Rejected     bool
	Reused       []DeclID
	Stats        Stats
}

type Logger interface {
	Log(event string, args ...any)
}

type nopLogger struct{}

func (nopLogger) Log(string, ...any) {}

func MissingSignature(id DeclID) Signature {
	return Signature{Value: "\x00missing:" + string(id), ErrorText: "declaration does not exist"}
}

func (b Basis) String() string {
	return fmt.Sprintf("%v", map[DeclID]BasisEntry(b))
}
