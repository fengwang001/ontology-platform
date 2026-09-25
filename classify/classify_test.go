package classify_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"ontology/classify"
	"ontology/timeout"
)

func TestOf(t *testing.T) {
	base := errors.New("boom")
	cases := []struct {
		name string
		err  error
		want classify.Kind
	}{
		{"nil 归为可重试占位", nil, classify.Retryable},
		{"普通错误可重试", base, classify.Retryable},
		{"超时哨兵", classify.ErrTimeout, classify.Timeout},
		{"包装超时", fmt.Errorf("wrap: %w", classify.ErrTimeout), classify.Timeout},
		{"context 截止", context.DeadlineExceeded, classify.Timeout},
		{"标记不可重试", classify.Mark(base), classify.NonRetryable},
		{"包装的标记", fmt.Errorf("wrap: %w", classify.Mark(base)), classify.NonRetryable},
		{"标记优先于默认", classify.Mark(errors.New("x")), classify.NonRetryable},
	}
	for _, c := range cases {
		if got := classify.Of(c.err); got != c.want {
			t.Errorf("%s: Of()=%v want %v", c.name, got, c.want)
		}
	}
	if classify.Mark(nil) != nil {
		t.Error("Mark(nil) 应返回 nil")
	}
}

func TestSentinelErrorsIs(t *testing.T) {
	sentinels := []error{
		classify.ErrTimeout, classify.ErrBulkheadFull,
		classify.ErrCircuitOpen, classify.ErrClockSkew,
	}
	for i, s := range sentinels {
		wrapped := fmt.Errorf("layer: %w", s)
		if !errors.Is(wrapped, s) {
			t.Errorf("errors.Is 应识别包装后的 %v", s)
		}
		for j, other := range sentinels {
			if i != j && errors.Is(s, other) {
				t.Errorf("%v 与 %v 不应互相匹配", s, other)
			}
		}
	}
}

func TestTimeoutDo(t *testing.T) {
	cases := []struct {
		name    string
		limit   time.Duration
		fn      func(context.Context) error
		wantErr error // nil 表示成功；否则用 errors.Is 判定
	}{
		{"成功", time.Second, func(context.Context) error { return nil }, nil},
		{"普通失败", time.Second, func(context.Context) error { return errors.New("x") }, nil},
		{"超时", 10 * time.Millisecond, func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}, classify.ErrTimeout},
		{"阻塞超时", 10 * time.Millisecond, func(context.Context) error {
			time.Sleep(time.Second)
			return nil
		}, classify.ErrTimeout},
		{"panic 转错误", time.Second, func(context.Context) error { panic("bang") }, nil},
	}
	for _, c := range cases {
		err := timeout.Do(context.Background(), c.limit, c.fn)
		switch {
		case c.wantErr != nil:
			if !errors.Is(err, c.wantErr) {
				t.Errorf("%s: 应匹配 %v, 得到 %v", c.name, c.wantErr, err)
			}
		case c.name == "普通失败" || c.name == "panic 转错误":
			if err == nil || errors.Is(err, classify.ErrTimeout) {
				t.Errorf("%s: 应为非超时错误, 得到 %v", c.name, err)
			}
		case err != nil:
			t.Errorf("%s: 应成功, 得到 %v", c.name, err)
		}
	}
	if err := timeout.Do(context.Background(), 0, func(context.Context) error { return nil }); err == nil {
		t.Error("非正时限应报错")
	}
}
