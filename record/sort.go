package record

import "sort"

func sortRecords(rs []Record) {
	sort.Slice(rs, func(i, j int) bool { return Less(rs[i], rs[j]) })
}
