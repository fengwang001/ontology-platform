package ontology

import (
	"math/big"
	"sort"
)

type normalizedQuery struct {
	names         []string
	rowCounts     []int64
	selectivities [][]*big.Rat
}

func normalizeQuery(query Query) (normalizedQuery, error) {
	if len(query.Tables) == 0 {
		return normalizedQuery{}, ErrEmptyQuery
	}
	if len(query.Tables) > 12 {
		return normalizedQuery{}, ErrTooManyTables
	}

	tables := append([]Table(nil), query.Tables...)
	sort.SliceStable(tables, func(i, j int) bool {
		return tables[i].Name < tables[j].Name
	})

	seenNames := make(map[string]struct{}, len(tables))
	for _, table := range tables {
		if table.Name == "" {
			return normalizedQuery{}, ErrEmptyTableName
		}
		if table.RowCount < 0 {
			return normalizedQuery{}, ErrNegativeRowCount
		}
		if _, exists := seenNames[table.Name]; exists {
			return normalizedQuery{}, ErrDuplicateTableName
		}
		seenNames[table.Name] = struct{}{}
	}

	indexByName := make(map[string]int, len(tables))
	names := make([]string, len(tables))
	rowCounts := make([]int64, len(tables))
	for i, table := range tables {
		indexByName[table.Name] = i
		names[i] = table.Name
		rowCounts[i] = table.RowCount
	}

	predicates := append([]Predicate(nil), query.Predicates...)
	sort.SliceStable(predicates, func(i, j int) bool {
		if predicates[i].LeftTable != predicates[j].LeftTable {
			return predicates[i].LeftTable < predicates[j].LeftTable
		}
		if predicates[i].RightTable != predicates[j].RightTable {
			return predicates[i].RightTable < predicates[j].RightTable
		}
		if predicates[i].Numerator != predicates[j].Numerator {
			return predicates[i].Numerator < predicates[j].Numerator
		}
		return predicates[i].Denominator < predicates[j].Denominator
	})

	for _, predicate := range predicates {
		_, leftKnown := indexByName[predicate.LeftTable]
		_, rightKnown := indexByName[predicate.RightTable]
		if !leftKnown || !rightKnown {
			return normalizedQuery{}, ErrUnknownPredicate
		}
	}
	for _, predicate := range predicates {
		if predicate.LeftTable == predicate.RightTable {
			return normalizedQuery{}, ErrSelfPredicate
		}
	}
	for _, predicate := range predicates {
		if predicate.Numerator <= 0 || predicate.Denominator <= 0 || predicate.Numerator > predicate.Denominator {
			return normalizedQuery{}, ErrInvalidSelectivity
		}
	}

	selectivities := make([][]*big.Rat, len(tables))
	for i := range selectivities {
		selectivities[i] = make([]*big.Rat, len(tables))
	}
	for _, predicate := range predicates {
		left := indexByName[predicate.LeftTable]
		right := indexByName[predicate.RightTable]
		if left > right {
			left, right = right, left
		}
		nextSelectivity := new(big.Rat).SetFrac(
			big.NewInt(predicate.Numerator),
			big.NewInt(predicate.Denominator),
		)
		if current := selectivities[left][right]; current == nil {
			selectivities[left][right] = nextSelectivity
		} else {
			current.Mul(current, nextSelectivity)
		}
	}

	return normalizedQuery{
		names:         names,
		rowCounts:     rowCounts,
		selectivities: selectivities,
	}, nil
}
