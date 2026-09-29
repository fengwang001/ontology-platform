package tso

import "strconv"

func formatTimestamp(t Timestamp) string {
	return "(" + strconv.FormatInt(t.Physical, 10) + "," +
		strconv.FormatInt(t.Logical, 10) + ")"
}

func formatBound(b Bound) string {
	return "(term=" + strconv.FormatInt(b.Term, 10) +
		",high=" + strconv.FormatInt(b.High, 10) + ")"
}
