package multipart_test

import "ontology/coalesce"

func iv(start, end int64) coalesce.Interval {
	return coalesce.Interval{Start: start, End: end}
}
