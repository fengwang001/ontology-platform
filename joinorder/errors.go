package joinorder

const (
	ReasonEmptyTableSet      = "empty_table_set"
	ReasonTooManyTables      = "too_many_tables"
	ReasonEmptyTableName     = "empty_table_name"
	ReasonDuplicateTableName = "duplicate_table_name"
	ReasonUnknownTable       = "unknown_table"
	ReasonSameTablePredicate = "same_table_predicate"
	ReasonInvalidSelectivity = "invalid_selectivity"
	ReasonNegativeRowCount   = "negative_row_count"
)

type ValidationError struct {
	Reason string
	Detail string
}

func (e ValidationError) Error() string {
	return e.Reason + ": " + e.Detail
}
