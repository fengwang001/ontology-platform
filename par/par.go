package par

import "ontology/table"

func Parse(buf []byte, k int) (*table.Table, error) {
	_ = buf
	_ = k
	return &table.Table{}, nil
}
