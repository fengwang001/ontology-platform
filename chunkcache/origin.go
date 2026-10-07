package chunkcache

import "context"

// FetchRequest 为一次源站取片请求：请求对象 Key 的切片下标 [First,Last]（含首含尾）。
// ExpectedVersion 非空时表示调用方期望的版本；源站版本不同时正常返回新版本数据。
type FetchRequest struct {
	Key             string
	First           int
	Last            int
	ExpectedVersion string
}

// FetchResult 为源站应答：实际版本、总长度、与下标一一对应的切片数据。
type FetchResult struct {
	Version string
	Length  int64
	Chunks  [][]byte
}

// Origin 是调用方注入的源站接口。
type Origin interface {
	Fetch(ctx context.Context, req FetchRequest) (FetchResult, error)
}

// OriginFunc 便于以函数形式提供源站。
type OriginFunc func(ctx context.Context, req FetchRequest) (FetchResult, error)

// Fetch 实现 Origin。
func (f OriginFunc) Fetch(ctx context.Context, req FetchRequest) (FetchResult, error) {
	return f(ctx, req)
}
