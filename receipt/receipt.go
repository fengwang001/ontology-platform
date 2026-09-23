// Package receipt 定义投递收据：消息 id 与投递世代号。
//
// 消息每经历一次投递（Receive 取出）世代号加一；任何使旧投递结束的
// 状态迁移（超时重投、进死信）都让旧世代的收据作废。
package receipt

// Receipt 是某一次投递的凭据。
type Receipt struct {
	MsgID      int64
	Generation int64
}

// Equal 报告两张收据是否指向同一次投递。
func (r Receipt) Equal(o Receipt) bool {
	return r.MsgID == o.MsgID && r.Generation == o.Generation
}
