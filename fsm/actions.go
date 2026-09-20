package fsm

// entryFunc 是进入某状态时执行的动作，可返回错误。
type entryFunc func() error

// exitFunc 是离开某状态时执行的动作。
type exitFunc func()

// runEntries 按注册顺序执行某状态的全部 entry 动作。
// 任一动作返回错误时立即停止并返回该错误。
func runEntries(funcs []entryFunc) error {
	for _, f := range funcs {
		if err := f(); err != nil {
			return err
		}
	}
	return nil
}

// runExits 按注册顺序执行某状态的全部 exit 动作。
func runExits(funcs []exitFunc) {
	for _, f := range funcs {
		f()
	}
}
