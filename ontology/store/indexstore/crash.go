package indexstore

// Crash 表示一次被精确注入的“进程崩溃”：当前进行中的持久化步骤及其后续
// 内存状态全部丢弃，只有此前已提交到 Disk 的内容保留。重新 Open 同一
// Disk 即等价于崩溃重启。
type Crash struct{}

func (Crash) Error() string { return crashPanic }

// Run 执行 fn；fn 内 CrashHook 触发的崩溃会被转成返回值 crash=true，
// 已提交的数据保留在 disk 上，随后可用 Open(disk, ...) 重启追赶。
// 其他 panic 不拦截。
func Run(disk *Disk, fn func(s *Store)) (crash bool) {
	defer func() {
		if r := recover(); r != nil {
			if r == crashPanic {
				crash = true
				return
			}
			panic(r)
		}
	}()
	s, err := Open(disk, Options{})
	if err != nil {
		panic(err)
	}
	fn(s)
	return false
}
