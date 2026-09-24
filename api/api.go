package api

import "ontology/keymap"

var (
	ErrEmptyKey     = keymap.ErrEmptyKey
	ErrKeyTooLong   = keymap.ErrKeyTooLong
	ErrTableFull    = keymap.ErrMapFull
	ErrInconsistent = keymap.ErrInconsistent
)

type Table struct {
	records *keymap.Map
}

func NewTable(maxKeyRunes, maxItems int) *Table {
	return &Table{records: keymap.New(maxKeyRunes, maxItems)}
}

func (t *Table) Put(key string, value any) error {
	return t.records.Put(key, value)
}

func (t *Table) Get(key string) (any, bool) {
	return t.records.Get(key)
}

func (t *Table) Keys() []string {
	return t.records.Keys()
}

func (t *Table) SelfCheck() error {
	return t.records.Check()
}
