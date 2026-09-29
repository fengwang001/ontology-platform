// Package split 用内容定义的谓词把完整字节流切成块的边界位置。
// 它是纯函数式的：只依赖输入字节，与数据如何分批到达无关。
package split

import (
	"errors"

	"ontology/roll"
)

// MaskBits 是边界掩码位数：窗口哈希低 12 位全零即判为内容边界。
const MaskBits = 12

var (
	// ErrMinMax 表示 min > max。
	ErrMinMax = errors.New("split: min must be <= max")
	// ErrWindow 表示窗口为 0 或窗口大于 min（与 ErrMinMax 是不同的错误）。
	ErrWindow = errors.New("split: window must be > 0 and <= min")
)

// Config 描述分块参数。
type Config struct {
	Min    int // 最小块长（最后一块可更短）
	Max    int // 最大块长，走到此长度强制切
	Window int // 滚动哈希窗口字节数
}

// Validate 判定参数是否合法，两类参数错误返回不同哨兵。
func (c Config) Validate() error {
	if c.Window <= 0 || c.Window > c.Min {
		return ErrWindow
	}
	if c.Min > c.Max {
		return ErrMinMax
	}
	return nil
}

// hit 是内容边界谓词：窗口哈希低 MaskBits 位全零。
func hit(h uint32) bool { return h&(1<<MaskBits-1) == 0 }

// Boundaries 返回 data 中所有块的右端边界（不含流末尾的残余）。
// 每个边界 b 表示一块 data[prev:b]；除最后一块外块长均在 [min,max] 内。
// 采用 DESIGN.md 的写法乙：从块起点第一个字节起照常推进哈希，仅在长度 < min
// 时不判边；在 max 处无论谓词如何强制切。
func Boundaries(data []byte, c Config) ([]int, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	rh, err := roll.New(c.Window)
	if err != nil {
		return nil, err
	}
	return Scan(rh, data, c)
}

// Scan 用调用方提供的滚动哈希器增量扫描 data，返回块右端位置，并在每个
// 边界处把哈希器复位（下一块从空窗口开始）。哈希器状态在调用间延续，因此
// 可以把一个字节流分任意多次喂入而得到与一次性喂入完全相同的边界。
func Scan(rh *roll.Hasher, data []byte, c Config) ([]int, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var bounds []int
	start := 0
	for i := 0; i < len(data); i++ {
		rh.Push(data[i])
		length := i + 1 - start
		if length < c.Min {
			continue
		}
		if length >= c.Max || (rh.Full() && hit(rh.Sum())) {
			bounds = append(bounds, i+1)
			start = i + 1
			rh.Reset()
		}
	}
	return bounds, nil
}
