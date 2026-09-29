// Package par splits a buffer at arbitrary offsets and parses in parallel.
package par

import "ontology/table"

// Parse splits buf into K chunks parsed concurrently and stitched together.
func Parse(buf []byte, k int, lim table.Limits) (table.Table, error) {
	return table.Table{}, nil
}
