package edgecache

import "context"

// Source 由调用方注入，表示源站。缓存只通过它回源。
type Source interface {
	// FetchChunks 取对象 key 的切片 [first, last]（含首含尾）。
	// expectVersion 为本次请求所依据的版本标识，空串表示调用方尚不知道版本。
	// 若 expectVersion 与源站现版本不同，源站正常返回新版本数据而非报错。
	// 成功时返回实际版本标识、对象总长度与这些切片的数据；
	// Data 按切片下标顺序排列，可因到达对象末尾而截短，最后一个切片可不足切片大小。
	FetchChunks(ctx context.Context, key string, first, last int64, expectVersion string) (SourceResult, error)
}

// SourceResult 是一次回源的成功应答。
type SourceResult struct {
	Version string
	Total   int64
	Data    [][]byte
}

// RangeKind 是字节范围的三种写法。
type RangeKind int

const (
	// RangeStartEnd 起止都给（含首含尾），终点超出总长度时截到末尾。
	RangeStartEnd RangeKind = iota
	// RangeFrom 只给起点，直到对象末尾。
	RangeFrom
	// RangeSuffix 只给末尾若干字节。
	RangeSuffix
)

// RangeSpec 描述一次请求的字节范围。
type RangeSpec struct {
	Kind  RangeKind
	Start int64 // RangeStartEnd / RangeFrom 的起点（含）
	End   int64 // RangeStartEnd 的终点（含）
	N     int64 // RangeSuffix 的末尾字节数
}

// Bytes 构造 [start, end]（含首含尾）范围。
func Bytes(start, end int64) RangeSpec {
	return RangeSpec{Kind: RangeStartEnd, Start: start, End: end}
}

// From 构造 [start, 末尾] 范围。
func From(start int64) RangeSpec {
	return RangeSpec{Kind: RangeFrom, Start: start}
}

// LastN 构造末尾 n 字节的范围。
func LastN(n int64) RangeSpec {
	return RangeSpec{Kind: RangeSuffix, N: n}
}

// ChunkInfo 标明应答中一个切片的来源。
type ChunkInfo struct {
	Index     int64
	FromCache bool // true 表示来自缓存（含由并发请求代为回源），false 表示本次请求回源
}

// Response 是一次范围请求的应答。
type Response struct {
	Key     string
	Version string
	Total   int64
	Start   int64 // Data 首字节在对象内的偏移
	Data    []byte
	Chunks  []ChunkInfo // 按切片下标升序
}
