package vanload

import "strings"

// Logger 操作日志接口：记录输入、输出与判定依据。
type Logger interface {
	Log(Event)
}

// Event 一次操作的结构化日志。
type Event struct {
	Op     string // load / batch_load / unload / query
	Input  string // 输入摘要
	Output string // 输出摘要
	Reason string // 判定依据摘要
}

// Option 系统配置项。
type Option func(*System)

// WithLogger 设置日志器。
func WithLogger(l Logger) Option {
	return func(sys *System) { sys.log = l }
}

// TextLogger 将日志以文本形式写入回调（便于测试捕获与打印）。
type TextLogger struct {
	write func(string)
}

// NewTextLogger 创建文本日志器，write 为每行日志的接收函数（如 fmt.Println）。
func NewTextLogger(write func(string)) *TextLogger {
	return &TextLogger{write: write}
}

// Log 实现 Logger。
func (l *TextLogger) Log(e Event) {
	if l != nil && l.write != nil {
		l.write("[" + e.Op + "] 输入: " + e.Input + " | 输出: " + e.Output + " | 依据: " + e.Reason)
	}
}

// 以下为日志摘要辅助函数，集中管理输出格式以便测试稳定。

func cargoInput(c Cargo) string {
	return "id=" + itoa(c.ID) +
		",重量=" + itoa(c.Weight) +
		",体积=" + itoa(c.Volume) +
		",停靠点=" + itoa(c.Stop) +
		",类别=" + c.Category.String()
}

func batchInput(cs []Cargo) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = cargoInput(c)
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

func rejectOutput(r *Reject) string {
	var b strings.Builder
	b.WriteString("拒绝:")
	b.WriteString(r.Kind.String())
	if r.FailedIndex >= 0 {
		b.WriteString(",失败件下标=")
		b.WriteString(itoa(r.FailedIndex))
	}
	if len(r.CompartmentReasons) > 0 {
		b.WriteString(",分区判定=[")
		first := true
		for k := 1; k <= len(r.CompartmentReasons); k++ {
			kind, ok := r.CompartmentReasons[k]
			if !ok {
				continue
			}
			if !first {
				b.WriteString(";")
			}
			first = false
			b.WriteString("分区")
			b.WriteString(itoa(k))
			b.WriteString(":")
			b.WriteString(kind.String())
		}
		b.WriteString("]")
	}
	return b.String()
}

func loadOutput(r LoadResult) string {
	return "成功:分区=" + itoa(r.Compartment)
}

func batchOutput(r BatchLoadResult) string {
	return "成功:放置数=" + itoa(len(r.Placements))
}

func unloadOutput(r UnloadResult) string {
	return r.Status.String() +
		",卸下=[" + strings.Join(intsToStrings(r.Removed), ",") + "]" +
		",已到达=" + itoa(r.ArrivedStop)
}

func reasonBasis(f feasibility) string {
	var b strings.Builder
	b.WriteString("归并原因=")
	b.WriteString(f.overall.String())
	b.WriteString("（严重度最低）；各分区首个失败=[")
	first := true
	for k := 1; k <= len(f.reasons); k++ {
		kind, ok := f.reasons[k]
		if !ok {
			continue
		}
		if !first {
			b.WriteString(";")
		}
		first = false
		b.WriteString("分区")
		b.WriteString(itoa(k))
		b.WriteString(":")
		b.WriteString(kind.String())
	}
	b.WriteString("]")
	return b.String()
}

func intsToStrings(a []int) []string {
	out := make([]string, len(a))
	for i, v := range a {
		out[i] = itoa(v)
	}
	return out
}
