package idb

// delivery 是一次待投递的事件（请求成功/失败或事务结束）。
// 事件在锁外按入队顺序逐个回调，回调中可再次调用公开 API，
// 等价于 IDB 同一事件循环上“完成事件 → 微任务自动提交”的时序。
type delivery struct {
	tx  *Transaction
	req *Request
	res *RequestResult
	err *Error

	terminal bool
	complete bool
}
