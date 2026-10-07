package ontology

import "context"

import "errors"

var ErrSimulatedCrash = errors.New("simulated crash")

type Value struct {
	Present bool
	Data    any
}

func Missing() Value { return Value{} }

func Explicit(data any) Value { return Value{Present: true, Data: data} }

type IndexKey struct {
	Present bool
	Data    any
}

type IndexDef struct {
	Name string
	Key  func(Value) IndexKey
	Fail bool
}

type PropertyDef struct {
	Name    string
	Default Value
	Indexes []IndexDef
}

type Schema struct {
	Properties map[string]PropertyDef
}

type PropertyWrite struct {
	Object string
	Value  Value
}

type IndexEntry struct {
	Index    string
	Property string
	Key      IndexKey
	Object   string
}

type EntryChange struct {
	Added   []IndexEntry
	Removed []IndexEntry
}

type Event struct {
	Op               string
	Object           string
	Property         string
	Writes           []PropertyWrite
	IndexChanges     map[string]EntryChange
	AttemptedChanges map[string]EntryChange
	Reason           string
	Committed        bool
	Recovered        bool
	TxID             uint64
}

type Logger interface {
	Log(Event)
}

type CrashPoint string

const (
	CrashAfterBegin   CrashPoint = "after_begin"
	CrashAfterData    CrashPoint = "after_data"
	CrashAfterIndex   CrashPoint = "after_index"
	CrashAfterPrepare CrashPoint = "after_prepare"
	CrashAfterCommit  CrashPoint = "after_commit"
)

type CrashHook func(context.Context, CrashPoint) bool
