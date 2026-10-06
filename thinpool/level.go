package thinpool

// Level 是池使用率水位档位。
type Level int

const (
	LevelNormal Level = iota
	LevelWarning
	LevelCritical
)

func (l Level) String() string {
	switch l {
	case LevelWarning:
		return "warning"
	case LevelCritical:
		return "critical"
	default:
		return "normal"
	}
}

// Event 是一次水位档位变化事件。
type Event struct {
	Seq       int   // 事件序号，从 1 连续递增
	From      Level // 旧档位
	To        Level // 新档位
	Allocated int   // 事件发生时已分配物理块数
}

// classify 以纯整数乘法比较使用率：
// allocated*100 >= physical*threshold 视为达到水位（取等升档）。
func classify(allocated, physical, warningPct, criticalPct int) Level {
	if physical <= 0 {
		return LevelNormal
	}
	hundred := allocated * 100
	if hundred >= physical*criticalPct {
		return LevelCritical
	}
	if hundred >= physical*warningPct {
		return LevelWarning
	}
	return LevelNormal
}
