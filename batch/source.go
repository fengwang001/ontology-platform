package batch

// FuncSource 以生成函数按需产生记录，避免大批次整体驻留内存。
type FuncSource struct {
	n    int
	make func(i int) Record
}

// NewFuncSource 创建记录数为 n 的函数式来源。
func NewFuncSource(n int, make func(i int) Record) *FuncSource {
	return &FuncSource{n: n, make: make}
}

// Count 返回记录总数。
func (s *FuncSource) Count() int { return s.n }

// At 返回第 i 条记录。
func (s *FuncSource) At(i int) (Record, error) { return s.make(i), nil }

// Close 释放资源，函数式来源为空操作。
func (s *FuncSource) Close() error { return nil }
