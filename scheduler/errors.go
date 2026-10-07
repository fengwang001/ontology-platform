package scheduler

// Kind 区分错误类别。同一操作内多个拒绝条件同时成立时，按声明顺序取先者：
// 参数非法 > 时钟回退 > 指令不存在或已取消 > 时段进行中 > 已过截止 > 通知不存在。
type Kind int

const (
	KindParam    Kind = iota // 参数非法
	KindClock                // 时钟回退
	KindInstr                // 指令不存在或已取消
	KindBusy                 // 时段进行中
	KindDeadline             // 已过截止
	KindNotif                // 通知不存在
)

// Error 是所有被拒绝操作返回的错误类型，Kind 可区分错误类别。
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func paramErr(msg string) *Error    { return &Error{KindParam, "参数非法: " + msg} }
func clockErr(msg string) *Error    { return &Error{KindClock, "时钟回退: " + msg} }
func instrErr(msg string) *Error    { return &Error{KindInstr, "指令不存在或已取消: " + msg} }
func busyErr(msg string) *Error     { return &Error{KindBusy, "时段进行中: " + msg} }
func deadlineErr(msg string) *Error { return &Error{KindDeadline, "已过截止: " + msg} }
func notifErr(msg string) *Error    { return &Error{KindNotif, "通知不存在: " + msg} }
