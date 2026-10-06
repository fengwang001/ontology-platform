// Package fontkernel 实现网页字体匹配、子集加载与回退渲染的协调内核。
//
// 五个相互协作的部分：
//   - 字体族登记（registry.go）：人脸声明、参数校验、重复登记拒绝；
//   - 人脸匹配（match.go）：宽度 → 倾斜 → 字重的三维匹配；
//   - 字符范围子集（tree.go）：基于质心区间树的字符覆盖查询；
//   - 加载状态期（engine.go）：阻塞期 / 交换期 / 永久回退的状态机；
//   - 文本整形（engine.go）：逐字符裁决并合并连续同人脸段落。
package fontkernel

// Style 为字形倾斜声明。
type Style int

const (
	StyleNormal Style = iota
	StyleItalic
)

// Policy 为资源显示策略（对应 CSS font-display）。
type Policy int

const (
	PolicyBlock Policy = iota
	PolicySwap
	PolicyFallback
	PolicyOptional
)

// Period 为字符当前所处的显示时期。
type Period int

const (
	PeriodBlock             Period = iota // 阻塞期：不可见占位
	PeriodSwap                            // 交换期：回退族渲染
	PeriodLoaded                          // 目标人脸已加载完成
	PeriodFallbackPermanent               // 永久回退（含可选策略放弃）
)

// FaceStatus 为一张人脸的子集加载状态。
type FaceStatus int

const (
	FaceUntriggered FaceStatus = iota // 尚未被任何文本触发
	FaceLoading                       // 已触发，资源加载中
	FaceLoaded                        // 加载完成（在放弃前完成）
	FaceFailed                        // 加载失败或超时永久放弃
	FaceLoadedLate                    // 放弃后才到达的完成（仅供观测，永不替换）
)

// RuneRange 是一个左闭右闭的码点区间。
type RuneRange struct {
	Lo rune
	Hi rune
}

// FaceSpec 声明一张字体人脸。
type FaceSpec struct {
	WeightLo int         // 字重区间下界（含）
	WeightHi int         // 字重区间上界（含）
	Style    Style       // 正常或斜体
	WidthLo  float64     // 宽度区间下界（含，100 为正常）
	WidthHi  float64     // 宽度区间上界（含）
	Runes    []RuneRange // 覆盖的字符范围（可为多个不相交区间）
	Policy   Policy      // 显示策略
	URL      string      // 资源地址
}

// FamilySpec 声明一个字体族。
type FamilySpec struct {
	Name  string
	Faces []FaceSpec
}

// Config 为内核可调参数（均以逻辑时钟刻度计）。
type Config struct {
	WeightLow   int     // 字重下阈值
	WeightHigh  int     // 字重上阈值（WeightLow <= WeightHigh）
	NormalWidth float64 // 正常宽度（默认 100）

	BlockBlockPeriod    int64 // 阻塞策略阻塞期（长）
	SwapBlockPeriod     int64 // 交换策略阻塞期（极短）
	FallbackBlockPeriod int64 // 回退策略阻塞期
	FallbackSwapPeriod  int64 // 回退策略交换期（有限）
	OptionalBlockPeriod int64 // 可选策略阻塞期（极短），无交换期
}

// ShapeRequest 是一次文本整形请求。
type ShapeRequest struct {
	Family   string   // 主字体族名
	Fallback []string // 回退族名（按优先次序）
	Text     string   // 待渲染文本
	Weight   int      // 请求字重
	Style    Style    // 请求倾斜
	Width    float64  // 请求宽度
}

// RuneResult 是单个字符的整形裁决。
type RuneResult struct {
	Rune            rune
	RenderFamily    string // 实际渲染所用族；最后手段时为空
	RenderFace      int    // 实际渲染人脸上标；最后手段时为 -1
	WinnerFamily    string // 主匹配胜出族
	WinnerFace      int    // 主匹配胜出人脸上标
	Period          Period
	SyntheticItalic bool
	LastResort      bool
}

// Segment 是连续使用同一渲染身份的字符合并段。
type Segment struct {
	Text            string
	Family          string
	Face            int
	Period          Period
	SyntheticItalic bool
	LastResort      bool
}

// ShapeResult 是整形输出。
type ShapeResult struct {
	Time     int64
	Runes    []RuneResult
	Segments []Segment
}

// FaceState 为一张人脸在某时刻的状态快照。
type FaceState struct {
	Family    string
	Face      int
	Status    FaceStatus
	TriggerAt int64 // 首次触发时刻；未触发为 -1
	LoadedAt  int64 // 完成时刻；未完成为 -1
}

// DefaultConfig 返回题目语义下的默认参数。
func DefaultConfig() Config {
	return Config{
		WeightLow:           400,
		WeightHigh:          500,
		NormalWidth:         100,
		BlockBlockPeriod:    3000,
		SwapBlockPeriod:     0,
		FallbackBlockPeriod: 100,
		FallbackSwapPeriod:  3000,
		OptionalBlockPeriod: 100,
	}
}
