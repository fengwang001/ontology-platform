package rank

import "strings"

// compareValues is the default value comparison: ascending by Value.
// +0.0 and -0.0 compare equal because neither is less than the other.
func compareValues(a, b Row) int {
	switch {
	case a.Value < b.Value:
		return -1
	case a.Value > b.Value:
		return 1
	default:
		return 0
	}
}

// compareIDs breaks ties by ascending row ID so that the order of tied
// rows is deterministic and independent of input order.
func compareIDs(a, b Row) int {
	return strings.Compare(a.ID, b.ID)
}

// fullCompare orders two rows for sorting: by value (honoring
// descending), then by ascending row ID.
func fullCompare(cfg Config, cmp func(a, b Row) int, a, b Row) int {
	c := cmp(a, b)
	if cfg.Descending {
		c = -c
	}
	if c == 0 {
		c = compareIDs(a, b)
	}
	return c
}
