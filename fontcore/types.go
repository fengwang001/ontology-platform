package fontcore

// Display 是字体显示策略，决定阻塞期/交换期的形态。
type Display int

const (
	DisplayBlock    Display = iota // 阻塞：长阻塞期，交换期无限
	DisplaySwap                    // 交换：极短阻塞期，交换期无限
	DisplayFallback                // 回退：两期均有限
	DisplayOptional                // 可选：极短阻塞期，无交换期，期满永久回退
)

// Style 为字形倾斜。
type Style int

const (
	StyleNormal Style = iota
	StyleItalic
)

// Phase 是字符当前所处的显示时期。
type Phase int

const (
	PhaseBlock      Phase = iota // 不可见占位
	PhaseSwap                    // 回退族渲染
	PhasePrimary                 // 主人脸渲染
	PhaseFailed                  // 永久回退（加载失败或策略期满）
	PhaseLastResort              // 无任何回退覆盖
)

func (p Phase) String() string {
	switch p {
	case PhaseBlock:
		return "block"
	case PhaseSwap:
		return "swap"
	case PhasePrimary:
		return "primary"
	case PhaseFailed:
		return "failed-fallback"
	default:
		return "last-resort"
	}
}

// RuneRange 是闭区间字符范围 [Lo, Hi]，rune 即 Unicode 码点。
type RuneRange struct {
	Lo rune
	Hi rune
}

// FaceSpec 描述一张待登记的人脸。
type FaceSpec struct {
	Name     string
	WeightLo int
	WeightHi int
	Style    Style
	WidthLo  int
	WidthHi  int
	Ranges   []RuneRange
	Display  Display
	Resource string
}

// FamilySpec 描述一个字体族及其回退族（按优先级排列的族名）。
type FamilySpec struct {
	Name      string
	Faces     []FaceSpec
	Fallbacks []string
}

// Config 为引擎级可配置参数（时间单位由时钟调用方约定，通常毫秒）。
type Config struct {
	WeightLow     int64 // 字重下阈值
	WeightHigh    int64 // 字重上阈值（>= 下阈值）
	BlockBlock    int64 // 阻塞策略的长阻塞期
	SwapBlock     int64 // 交换策略的极短阻塞期
	FallbackBlock int64 // 回退策略阻塞期
	FallbackSwap  int64 // 回退策略交换期
	OptionalBlock int64 // 可选策略的极短阻塞期
}

// DefaultConfig 给出与 CSS font-display 量级相近的默认值（毫秒）。
func DefaultConfig() Config {
	return Config{
		WeightLow:     400,
		WeightHigh:    500,
		BlockBlock:    3000,
		SwapBlock:     0,
		FallbackBlock: 100,
		FallbackSwap:  3000,
		OptionalBlock: 100,
	}
}

// Run 是整形输出中的一个连续段落。
type Run struct {
	Family          string
	Face            string
	Phase           Phase
	SyntheticItalic bool
	Start           int // 字节起始偏移
	End             int // 字节结束偏移（左闭右开）
	Runes           int // 包含的字符数
}

// CharResult 是单个字符的整形结果（测试与朴素模型对照用）。
type CharResult struct {
	Family          string
	Face            string
	Phase           Phase
	SyntheticItalic bool
}
