// Package classify 按固定优先级规则把信令消息分为 Exempt / Low / Normal 三级。
package classify

import "errors"

// ErrInvalidParam 表示消息参数非法（未知 Method 或未知 Priority）。
var ErrInvalidParam = errors.New("classify: invalid parameter")

// Priority 为消息携带的优先级。
type Priority string

const (
	PriorityEmergency Priority = "emergency"
	PriorityNormal    Priority = "normal"
	PriorityNonUrgent Priority = "non-urgent"
)

// Class 为消息分级结果。
type Class int

const (
	// Exempt 不可减载，一律转发。
	Exempt Class = iota
	// Low 在欠额达到 θlow 时被减载。
	Low
	// Normal 在欠额达到 θnorm 时被减载。
	Normal
)

func (c Class) String() string {
	switch c {
	case Exempt:
		return "Exempt"
	case Low:
		return "Low"
	case Normal:
		return "Normal"
	}
	return "Unknown"
}

// Message 为待分级的信令消息。
type Message struct {
	Method   string
	InDialog bool
	Priority Priority
}

var exemptMethods = map[string]bool{
	"ACK": true, "BYE": true, "CANCEL": true, "PRACK": true,
}

var lowMethods = map[string]bool{
	"OPTIONS": true, "SUBSCRIBE": true, "MESSAGE": true,
}

var normalMethods = map[string]bool{
	"INVITE": true, "REGISTER": true, "UPDATE": true, "REFER": true,
	"NOTIFY": true, "PUBLISH": true, "INFO": true,
}

func validMethod(m string) bool {
	return exemptMethods[m] || lowMethods[m] || normalMethods[m]
}

func validPriority(p Priority) bool {
	switch p {
	case PriorityEmergency, PriorityNormal, PriorityNonUrgent:
		return true
	}
	return false
}

// Classify 按序判定消息级别，先中先得。
// Method 非法时返回 ErrInvalidParam（即使 InDialog 为真）。
func Classify(m Message) (Class, error) {
	if !validMethod(m.Method) {
		return 0, ErrInvalidParam
	}
	if !validPriority(m.Priority) {
		return 0, ErrInvalidParam
	}
	if m.InDialog || m.Priority == PriorityEmergency || exemptMethods[m.Method] {
		return Exempt, nil
	}
	if m.Priority == PriorityNonUrgent || lowMethods[m.Method] {
		return Low, nil
	}
	return Normal, nil
}
