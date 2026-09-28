package asyncbuf

// Kind 标识输出事件的种类。
type Kind int

const (
	// ElementEvent 表示元素输出事件。
	ElementEvent Kind = iota
	// WatermarkEvent 表示水位线输出事件。
	WatermarkEvent
)

// Event 是一次可输出事件：元素或水位线。
type Event struct {
	Kind Kind
	// ID 仅对元素事件有效，为元素标识。
	ID string
	// Seq 仅对水位线事件有效，为水位线序号。
	Seq int64
	// Order 是该事件的输入序号（从 0 起，元素与水位线共用），
	// 便于与朴素模拟逐条对照。
	Order int
}

// IsWatermark 报告该事件是否为水位线。
func (e Event) IsWatermark() bool { return e.Kind == WatermarkEvent }
