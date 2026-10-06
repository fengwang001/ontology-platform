package bus

// Scheme 描述一条公交线路的静态方案。
// 所有时长字段均为正整数秒（在 NewService 中校验）。
type Scheme struct {
	// Stops 为顺序站点，Control 标记控制站。
	Stops []StopSpec
	// Travel[i] 为 Stops[i] 到 Stops[i+1] 的计划行驶时长，长度 = len(Stops)-1。
	Travel []int64
	// HeadwaySec 目标发车间隔 H。
	HeadwaySec int64
	// HoldCapSec 单次扣车上限。
	HoldCapSec int64
	// ToleranceSec 控制站最晚允许离站 = 计划到站 + 容忍量。
	ToleranceSec int64
	// DutyCapSec 司机连续在岗时长上限。
	DutyCapSec int64
}

// StopSpec 描述单个站点。
type StopSpec struct {
	Name string
	// Control 标记控制站。
	Control bool
	// DwellSec 本站计划停站时长。
	DwellSec int64
}

func (s Scheme) validate() error {
	if len(s.Stops) < 2 {
		return kindError(ErrInvalidParam, "scheme requires at least 2 stops")
	}
	seenName := map[string]bool{}
	for i, st := range s.Stops {
		if st.Name == "" || seenName[st.Name] {
			return kindError(ErrInvalidParam, "stop name empty or duplicated")
		}
		seenName[st.Name] = true
		if st.DwellSec <= 0 {
			return kindError(ErrInvalidParam, "dwell must be positive")
		}
		_ = i
	}
	if len(s.Travel) != len(s.Stops)-1 {
		return kindError(ErrInvalidParam, "travel length must be stops-1")
	}
	for _, d := range s.Travel {
		if d <= 0 {
			return kindError(ErrInvalidParam, "travel must be positive")
		}
	}
	if s.HeadwaySec <= 0 || s.HoldCapSec <= 0 || s.ToleranceSec <= 0 || s.DutyCapSec <= 0 {
		return kindError(ErrInvalidParam, "headway/holdCap/tolerance/dutyCap must be positive")
	}
	return nil
}

// StopCount 返回站点数。
func (s Scheme) StopCount() int { return len(s.Stops) }

// IsControl 判断站点是否为控制站。
func (s Scheme) IsControl(stop int) bool {
	return stop >= 0 && stop < len(s.Stops) && s.Stops[stop].Control
}

// NextControl 返回 stop 之后第一个控制站的下标，不存在返回 -1。
func (s Scheme) NextControl(stop int) int {
	for i := stop + 1; i < len(s.Stops); i++ {
		if s.Stops[i].Control {
			return i
		}
	}
	return -1
}

// planArrival 依据锚点（stop0 的计划到站时刻）推导 stop 的计划到站时刻。
func (s Scheme) planArrival(anchor int64, stop int) int64 {
	t := anchor
	for i := 0; i < stop; i++ {
		t += s.Stops[i].DwellSec + s.Travel[i]
	}
	return t
}
