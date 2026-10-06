package idb

// 同步风格的便捷封装：在 Hold/Unhold 之间发出的请求不会在请求间隙被
// 自动提交；Unhold 后若无其他在途请求则进入自动提交，Wait 等待结束。
// 回调风格（Request.OnSuccess/OnError 中继续发请求）是更贴近 IDB 的
// 原生用法，两者等价。

// SyncGet 在 hold 期间同步读取：返回值与“是否存在”。
func SyncGet(t *Transaction, store, key string) (string, bool, error) {
	type outcome struct {
		v   string
		ok  bool
		err *Error
	}
	done := make(chan outcome, 1)
	_, err := t.GetEx(store, []byte(key),
		WithOnSuccess(func(res *RequestResult) {
			done <- outcome{v: string(res.Value), ok: res.Exists}
		}),
		WithOnError(func(e *Error) { done <- outcome{err: e} }),
	)
	if err != nil {
		return "", false, err
	}
	o := <-done
	if o.err != nil {
		return "", false, o.err
	}
	return o.v, o.ok, nil
}

// SyncPut 在 hold 期间同步写入。
func SyncPut(t *Transaction, store, key, value string, addOnly bool) error {
	done := make(chan *Error, 1)
	_, err := t.PutEx(store, []byte(key), []byte(value), addOnly,
		WithOnSuccess(func(*RequestResult) { done <- nil }),
		WithOnError(func(e *Error) { done <- e }),
	)
	if err != nil {
		return err
	}
	if e := <-done; e != nil {
		return e
	}
	return nil
}

// SyncDelete 在 hold 期间同步删除。
func SyncDelete(t *Transaction, store, key string) error {
	done := make(chan *Error, 1)
	_, err := t.DeleteEx(store, []byte(key),
		WithOnSuccess(func(*RequestResult) { done <- nil }),
		WithOnError(func(e *Error) { done <- e }),
	)
	if err != nil {
		return err
	}
	if e := <-done; e != nil {
		return e
	}
	return nil
}

// WithTx 在一个显式 hold 的事务内执行 fn；fn 返回 nil 时自动提交，
// 返回错误时显式中止并返回该错误。
func WithTx(c *Connection, mode Mode, stores []string, fn func(t *Transaction) error) error {
	return withTx(c, mode, stores, fn)
}

func waitTxRunning(t *Transaction) bool {
	return t.waitStart()
}

func withTx(c *Connection, mode Mode, stores []string, fn func(t *Transaction) error) error {
	k := c.db.k
	k.Pause()
	t, err := c.Transaction(mode, stores)
	if err != nil {
		k.Resume()
		return err
	}
	if err := t.Hold(); err != nil {
		k.Resume()
		return err
	}
	k.Resume()
	// 若事务创建时被调度排队（例如有重叠事务在运行），等待其开始。
	if !waitTxRunning(t) {
		return KindTxInactive.New("transaction ended before it started")
	}
	ferr := fn(t)
	if ferr != nil {
		_ = t.Unhold()
		_ = t.Abort()
		_ = t.Wait()
		return ferr
	}
	if err := t.Unhold(); err != nil {
		return err
	}
	return t.Wait()
}
