package backupretention

// Layer 标识保留层。
type Layer int

const (
	// LayerDaily 日层：周期为 UTC 整日。
	LayerDaily Layer = iota + 1
	// LayerWeekly 周层：周期为周一开始的 UTC 周。
	LayerWeekly
	// LayerMonthly 月层：周期为公历月（UTC）。
	LayerMonthly
)

// MaxLayerCount 单层允许保留的最大周期数量。
const MaxLayerCount = 1000

// Policy 给出日 / 周 / 月三层各自保留的最近周期数量，取值 0..1000。
// 0 表示该层不保留任何备份。
type Policy struct {
	Daily   int
	Weekly  int
	Monthly int
}

// validate 仅做层数量范围检查（0..MaxLayerCount），时钟检查由 Service 负责。
// 返回 *ServiceError，类别固定为 KindLayerOutOfRange。
func (p Policy) validate() error {
	if p.Daily < 0 || p.Daily > MaxLayerCount ||
		p.Weekly < 0 || p.Weekly > MaxLayerCount ||
		p.Monthly < 0 || p.Monthly > MaxLayerCount {
		return newError(KindLayerOutOfRange, "SetPolicy",
			"retention counts must be within [0,1000]")
	}
	return nil
}

// count 返回指定层的保留数量。
func (p Policy) count(layer Layer) int {
	switch layer {
	case LayerDaily:
		return p.Daily
	case LayerWeekly:
		return p.Weekly
	case LayerMonthly:
		return p.Monthly
	default:
		return 0
	}
}
