// Package par splits a buffer into K chunks parsed in parallel.
package par

import (
	"ontology/table"
)

// Parse parses buf using K goroutines, with arbitrary split offsets.
func Parse(buf []byte, k int, opts ...table.Option) (*table.Table, error) {
	return nil, nil
}

// BytesProcessed reports summed state-machine byte handling across workers.
func BytesProcessed() int { return 0 }
