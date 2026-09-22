// Package segment 实现列式存储段的写入与只读视图。
//
// 段是不可变的字节序列：文件头 + 行组索引 + 若干行组块。
// 每个行组块内部依次为：头部（行数/空值数/编码/字典）、
// 统计（min/max）、空值位图、数据块（位打包的值或字典码字）。
// 空值用位图表示，与数值 0 是两种可判定的读取结果；
// 统计中的 min/max 只来自非空值。
package segment

import (
	"errors"
	"fmt"
)

// Encoding 是行组的数据编码类型。
type Encoding byte

const (
	// EncBitpack 表示值经 min 偏移后直接位打包。
	EncBitpack Encoding = 1
	// EncDict 表示值经字典编码，码字位打包。
	EncDict Encoding = 2
)

func (e Encoding) String() string {
	switch e {
	case EncBitpack:
		return "bitpack"
	case EncDict:
		return "dict"
	}
	return fmt.Sprintf("unknown(%d)", byte(e))
}

// Stage 标识行组块内的解析阶段，用于定位损坏位置。
type Stage int

const (
	// StageHeader 段头/行组头（含字典）。
	StageHeader Stage = iota
	// StageStats 行组统计区。
	StageStats
	// StageNullBitmap 空值位图。
	StageNullBitmap
	// StageData 数据块。
	StageData
)

func (s Stage) String() string {
	switch s {
	case StageHeader:
		return "header"
	case StageStats:
		return "stats"
	case StageNullBitmap:
		return "null-bitmap"
	case StageData:
		return "data"
	}
	return "unknown"
}

// CorruptError 表示段字节损坏。Group 为出错的行组号，
// -1 表示段级头部；Stage 为失败阶段。
type CorruptError struct {
	Group int
	Stage Stage
	Msg   string
}

func (e *CorruptError) Error() string {
	if e.Group < 0 {
		return fmt.Sprintf("segment corrupt at segment %s: %s", e.Stage, e.Msg)
	}
	return fmt.Sprintf("segment corrupt at group %d %s: %s", e.Group, e.Stage, e.Msg)
}

// IsCorrupt 报告 err 是否为（或包装了）损坏错误。
func IsCorrupt(err error) bool {
	var ce *CorruptError
	return errors.As(err, &ce)
}

// 资源上限错误，三者彼此可判定。
var (
	// ErrTooManyRows 单段行数超限。
	ErrTooManyRows = errors.New("segment: too many rows")
	// ErrTooManyRowGroups 行组数超限。
	ErrTooManyRowGroups = errors.New("segment: too many row groups")
	// ErrGroupOutOfRange 行组号越界。
	ErrGroupOutOfRange = errors.New("segment: group index out of range")
)

// Value 是读出的一行：Null 为真表示空值，此时 V 无意义。
// 空值、数值 0 是两种可判定的结果。
type Value struct {
	V    int64
	Null bool
}

// Options 是建段配置，零值字段取默认值。
type Options struct {
	RowGroupRows int // 每个行组的行数，默认 512
	MaxRows      int // 单段最大行数，默认 1<<28
	MaxRowGroups int // 最大行组数，默认 1<<20
	MaxDictCard  int // 字典最大基数，默认 4096；超限回退位打包
}

func (o Options) withDefaults() Options {
	if o.RowGroupRows <= 0 {
		o.RowGroupRows = 512
	}
	if o.MaxRows <= 0 {
		o.MaxRows = 1 << 28
	}
	if o.MaxRowGroups <= 0 {
		o.MaxRowGroups = 1 << 20
	}
	if o.MaxDictCard <= 0 {
		o.MaxDictCard = 4096
	}
	return o
}
