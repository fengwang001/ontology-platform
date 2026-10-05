// Package classify 按 SIP 方法、会话状态与优先级把消息分为 Exempt/Low/Normal 三级。
// 分级是纯函数：同样的消息永远得到同样的级别。
package classify

import "errors"

// ErrInvalidParam 表示参数非法（未知 Method 或未知 Priority）。
var ErrInvalidParam = errors.New("classify: invalid parameter")

// Priority 为消息优先级。
type Priority int

const (
	PriorityEmergency Priority = iota
	PriorityNormal
	PriorityNonUrgent
)

// Class 为减载分级。
type Class int

const (
	ClassExempt Class = iota // 豁免：一律转发
	ClassLow                 // 低优先：先被减载
	ClassNormal              // 普通：达到更高门槛才被减载
)

func (c Class) String() string {
	switch c {
	case ClassExempt:
		return "Exempt"
	case ClassLow:
		return "Low"
	default:
		return "Normal"
	}
}

// Message 为待分级消息。
type Message struct {
	Method   string
	InDialog bool
	Priority Priority
}

// 14 种合法 SIP 方法。
var validMethods = map[string]bool{
	"INVITE": true, "ACK": true, "BYE": true, "CANCEL": true,
	"REGISTER": true, "OPTIONS": true, "SUBSCRIBE": true, "NOTIFY": true,
	"MESSAGE": true, "PRACK": true, "UPDATE": true, "REFER": true,
	"PUBLISH": true, "INFO": true,
}

var exemptMethods = map[string]bool{
	"ACK": true, "BYE": true, "CANCEL": true, "PRACK": true,
}

var lowMethods = map[string]bool{
	"OPTIONS": true, "SUBSCRIBE": true, "MESSAGE": true,
}

// Classify 按序判定，先中先得：
//  1. InDialog 为真、或 Priority 为 emergency、或 Method ∈ {ACK,BYE,CANCEL,PRACK} → Exempt
//  2. Priority 为 non-urgent、或 Method ∈ {OPTIONS,SUBSCRIBE,MESSAGE} → Low
//  3. 其余 → Normal
//
// Method 非法时直接报 ErrInvalidParam，即使 InDialog 为真。
func Classify(m Message) (Class, error) {
	if !validMethods[m.Method] {
		return 0, ErrInvalidParam
	}
	switch m.Priority {
	case PriorityEmergency, PriorityNormal, PriorityNonUrgent:
	default:
		return 0, ErrInvalidParam
	}
	if m.InDialog || m.Priority == PriorityEmergency || exemptMethods[m.Method] {
		return ClassExempt, nil
	}
	if m.Priority == PriorityNonUrgent || lowMethods[m.Method] {
		return ClassLow, nil
	}
	return ClassNormal, nil
}
