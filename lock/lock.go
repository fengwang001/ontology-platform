// Package lock 实现对象版本上的保留期（GOVERNANCE/COMPLIANCE）与法律保留，
// 二者相互独立。它是纯状态机：不做参数/权限/时钟回退检查，
// 那些拒绝次序由上层 bucket 统一编排。
package lock

// Mode 是保留模式。
type Mode int

const (
	NONE Mode = iota
	GOVERNANCE
	COMPLIANCE
)

// L 是一个数据版本上的锁状态：保留模式/到期时刻与法律保留相互独立。
type L struct {
	Mode  Mode
	Until int64
	Hold  bool
}

// Result 是 SetRetention 的判定结果。
type Result int

const (
	OK Result = iota
	ErrCompliance
	ErrGovernance
)

// Active 报告保留期在 now 时刻是否仍生效：now 严格小于 Until。
// now == Until 视为已到期；NONE 与已到期保留均视同无保留。
func (l L) Active(now int64) bool { return l.Mode != NONE && now < l.Until }

func (l L) HasHold() bool { return l.Hold }

// SetRetention 按迁移规则改写保留期，返回判定结果。
// 调用方保证参数合法、且在调用前已完成权限/版本检查。
// 无保留或已到期：任何设置允许。
// 生效 COMPLIANCE：只允许仍为 COMPLIANCE 且 until 不小于原值。
// 生效 GOVERNANCE：改为 GOVERNANCE 或 COMPLIANCE 且 until 不小于原值时允许；
// 其余（缩短 until、改 NONE 清除、任何 until 变小）需 bypass 为真。
func (l *L) SetRetention(mode Mode, until, now int64, bypass bool) Result {
	allowed := true
	if l.Active(now) {
		switch l.Mode {
		case COMPLIANCE:
			allowed = mode == COMPLIANCE && until >= l.Until
			if !allowed {
				return ErrCompliance
			}
		case GOVERNANCE:
			allowed = (mode == GOVERNANCE || mode == COMPLIANCE) && until >= l.Until
			if !allowed && !bypass {
				return ErrGovernance
			}
		}
	}
	if mode == NONE {
		l.Mode = NONE
		l.Until = 0
	} else {
		l.Mode = mode
		l.Until = until
	}
	return OK
}

// SetHold 独立开关法律保留，不受保留期影响。
func (l *L) SetHold(on bool) { l.Hold = on }
