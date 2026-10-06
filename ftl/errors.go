package ftl

import "errors"

// 错误只报次序最靠前的一类：参数非法 > 未写入 > 空间耗尽。
var (
	// ErrInvalidArgument 逻辑页号越界等参数错误。
	ErrInvalidArgument = errors.New("ftl: invalid argument")
	// ErrNotWritten 读取未写过或已被丢弃的逻辑页。
	ErrNotWritten = errors.New("ftl: logical page not written")
	// ErrNoSpace 准入失败：已映射逻辑页数将达到上限。
	ErrNoSpace = errors.New("ftl: space exhausted")
)
