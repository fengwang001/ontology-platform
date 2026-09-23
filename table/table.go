package table

type Table struct{}

func Parse(p []byte) (*Table, error) { return &Table{}, nil }
