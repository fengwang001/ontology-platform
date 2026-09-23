// Package split 用内容定义谓词与 min/max 约束把字节流切成块。
package split

import (
	"errors"

	"ontology/roll"
)

var (
	// ErrMinMax 表示 min>max（或 min<1）。
	ErrMinMax = errors.New("split: require 1 <= min <= max")
	// ErrWindow 表示窗口长度为 0 或大于 min（与 ErrMinMax 不同的错误）。
	ErrWindow = errors.New("split: require 1 <= window <= min")
	// ErrBits 表示边界谓词位数非法。
	ErrBits = errors.New("split: bits must be in [1,31]")
)

// Config 是分块参数。
type Config struct {
	Window int // 滚动哈希窗口长，必须满足 1 <= Window <= Min
	Min    int // 最小块长（末块特例除外，见 DESIGN.md 第 4 节）
	Max    int // 最大块长
	Bits   int // 谓词 h%2^Bits==0，期望块长 2^Bits
}

// Span 是一块在源流中的半开区间 [Start,End)。
type Span struct {
	Start int64
	End   int64
}

// Splitter 对完整字节流执行内容定义分块。
type Splitter struct {
	cfg  Config
	mask uint64
}

// New 校验配置并创建分块器。
func New(cfg Config) (*Splitter, error) {
	if cfg.Min < 1 || cfg.Min > cfg.Max {
		return nil, ErrMinMax
	}
	if cfg.Window < 1 || cfg.Window > cfg.Min {
		return nil, ErrWindow
	}
	if cfg.Bits < 1 || cfg.Bits > 31 {
		return nil, ErrBits
	}
	return &Splitter{cfg: cfg, mask: uint64(1)<<uint(cfg.Bits) - 1}, nil
}

// Cut 返回 data 的块区间。
// 采用 DESIGN.md 第 1 节的乙法：滚动哈希自流头连续喂入，从不按块起点重启，
// 位置 <min 只禁止判边，切点因此是流上的内容固有标记。
func (s *Splitter) Cut(data []byte) []Span {
	if len(data) == 0 {
		return nil
	}
	h, _ := roll.New(s.cfg.Window)
	var spans []Span
	start := 0
	for i := 0; i < len(data); i++ {
		h.Push(data[i])
		off := i - start + 1 // 当前位置距块起点的字节数
		if off < s.cfg.Min || !h.Full() {
			continue
		}
		if off >= s.cfg.Max || h.Hash()&s.mask == 0 {
			spans = append(spans, Span{int64(start), int64(i + 1)})
			start = i + 1
		}
	}
	// 末尾处理（DESIGN.md 第 4 节）：残留 <min 并入末块；整流 <min 单独成块。
	if start < len(data) {
		if start == 0 {
			spans = append(spans, Span{0, int64(len(data))})
		} else if len(data)-start < s.cfg.Min {
			spans[len(spans)-1].End = int64(len(data))
		} else {
			spans = append(spans, Span{int64(start), int64(len(data))})
		}
	}
	return spans
}
