package indexorder

import "sync"

type Catalog struct {
	mu      sync.RWMutex
	indexes map[string]Index
}

func NewCatalog() *Catalog {
	return &Catalog{indexes: map[string]Index{}}
}

func (c *Catalog) Register(idx Index) error {
	if idx.Name == "" {
		return ErrEmptyName
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.indexes[idx.Name]; exists {
		return ErrDuplicateName
	}
	if len(idx.Column) == 0 {
		return ErrEmptyColumns
	}
	if err := validateColumns(idx.Column); err != nil {
		return err
	}
	c.indexes[idx.Name] = cloneIndex(idx)
	return nil
}

func (c *Catalog) Drop(name string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.indexes[name]; !exists {
		return ErrIndexNotFound
	}
	delete(c.indexes, name)
	return nil
}

func (c *Catalog) Choose(eq map[string]struct{}, order []ColumnItem) (Choice, error) {
	for col := range eq {
		if col == "" {
			return Choice{}, ErrEqEmptyColumnName
		}
	}
	for _, item := range order {
		if item.Column == "" {
			return Choice{}, ErrOrderEmptyColumnName
		}
	}
	for _, item := range order {
		if !validDirection(item.Dir) {
			return Choice{}, ErrOrderInvalidDirection
		}
	}
	for _, item := range order {
		if !validNulls(item.Nulls) {
			return Choice{}, ErrOrderInvalidNulls
		}
	}
	need := normalizeOrder(eq, order)
	c.mu.RLock()
	defer c.mu.RUnlock()
	var bestName string
	bestCols := 0
	bestTier := 0
	found := false
	for _, idx := range c.indexes {
		tier := -1
		if len(need) == 0 || satisfies(idx, eq, need, ScanForward) {
			tier = 0
		} else if satisfies(idx, eq, need, ScanBackward) {
			tier = 1
		}
		if tier < 0 {
			continue
		}
		if !found || tier < bestTier ||
			(tier == bestTier && len(idx.Column) < bestCols) ||
			(tier == bestTier && len(idx.Column) == bestCols && idx.Name < bestName) {
			found = true
			bestName = idx.Name
			bestCols = len(idx.Column)
			bestTier = tier
		}
	}
	if !found {
		return Choice{}, ErrNoMatchingIndex
	}
	scan := ScanForward
	if bestTier == 1 {
		scan = ScanBackward
	}
	return Choice{IndexName: bestName, Scan: scan}, nil
}

func cloneIndex(idx Index) Index {
	cols := make([]ColumnItem, len(idx.Column))
	copy(cols, idx.Column)
	return Index{Name: idx.Name, Column: cols}
}
