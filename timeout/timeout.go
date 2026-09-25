// Package timeout 为单次调用提供时限控制，并把 panic 捕获为失败。
package timeout

import (
	"errors"
	"fmt"
	"time"
)

// ErrTimeout 单次调用超时，可用 errors.Is 判定。
var ErrTimeout = errors.New("call timeout")

// Do 在时限 d 内执行 fn；超时返回 ErrTimeout，panic 被捕获并转为错误返回。
func Do(d time.Duration, fn func() error) error {
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic recovered: %v", r)
			}
		}()
		done <- fn()
	}()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case err := <-done:
		return err
	case <-t.C:
		return ErrTimeout
	}
}
