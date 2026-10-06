package store

type Table struct {
	rows   map[string]Row
	owners map[string]string
}

func NewTable() *Table {
	return &Table{rows: make(map[string]Row), owners: make(map[string]string)}
}

func (t *Table) Get(primary string) (Row, bool) {
	row, ok := t.rows[primary]
	return cloneRow(row), ok
}

func (t *Table) Put(row Row) {
	if existing, ok := t.rows[row.Primary]; ok && existing.Secondary != nil {
		delete(t.owners, *existing.Secondary)
	}
	t.rows[row.Primary] = cloneRow(row)
	if row.Secondary != nil {
		t.owners[*row.Secondary] = row.Primary
	}
}

func (t *Table) Delete(primary string) bool {
	row, ok := t.rows[primary]
	if !ok {
		return false
	}
	if row.Secondary != nil {
		delete(t.owners, *row.Secondary)
	}
	delete(t.rows, primary)
	return true
}

func (t *Table) Owner(secondary string) (string, bool) {
	primary, ok := t.owners[secondary]
	return primary, ok
}

func (t *Table) Snapshot() map[string]Row {
	result := make(map[string]Row, len(t.rows))
	for primary, row := range t.rows {
		result[primary] = cloneRow(row)
	}
	return result
}

func (t *Table) Len() int {
	return len(t.rows)
}

func cloneRow(row Row) Row {
	return Row{Primary: row.Primary, Secondary: cloneStringPtr(row.Secondary)}
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
