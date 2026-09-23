package u16

// Pump 排空“高代理后跟非低代理”后需要重新解释的单元。
// 返回 ok=false 表示没有待重新解释的事件。
func (d *Decoder) Pump() (Event, bool) {
	if !d.qReady {
		return Event{}, false
	}
	d.qReady = false
	return d.unitEvent(d.qUnit), true
}
