package serve_test

import (
	"errors"
	"testing"

	"ontology/coalesce"
	"ontology/serve"
	"ontology/source"
)

func TestTooManyRangesLeavesStateUntouched(t *testing.T) {
	data := makeData(1000)
	a := serve.New(serve.Config{MaxRanges: 2})

	// 先成功构建一个合法响应，建立已有状态。
	if err := a.Build("bytes=0-9", source.NewMemory(data)); err != nil {
		t.Fatal(err)
	}
	before := append([]coalesce.Interval(nil), a.Ranges()...)
	beforeSize := a.TotalSize()

	err := a.Build("bytes=0-1,2-3,4-5", source.NewMemory(data))
	if !errors.Is(err, serve.ErrTooManyRanges) {
		t.Fatalf("want ErrTooManyRanges, got %v", err)
	}
	if a.TotalSize() != beforeSize || len(a.Ranges()) != len(before) {
		t.Fatal("rejected build must not change existing state")
	}
}

func TestResponseTooLargeLeavesStateUntouched(t *testing.T) {
	data := makeData(1000)
	a := serve.New(serve.Config{MaxResponseBytes: 50})
	if err := a.Build("bytes=0-9", source.NewMemory(data)); err != nil {
		t.Fatal(err)
	}
	err := a.Build("bytes=0-199", source.NewMemory(data))
	if !errors.Is(err, serve.ErrResponseTooLarge) {
		t.Fatalf("want ErrResponseTooLarge, got %v", err)
	}
	if a.TotalSize() != 10 || a.Written() != 0 {
		t.Fatal("state changed despite size rejection")
	}
}

func TestThreeLimitErrorsAreDistinct(t *testing.T) {
	if errors.Is(serve.ErrTooManyRanges, serve.ErrResponseTooLarge) {
		t.Fatal("range limit and size limit must be distinguishable")
	}
	if errors.Is(serve.ErrTooManyRanges, serve.ErrBoundaryAttempts) ||
		errors.Is(serve.ErrResponseTooLarge, serve.ErrBoundaryAttempts) {
		t.Fatal("boundary limit must be distinguishable")
	}
}

func TestBoundaryAttemptsLimit(t *testing.T) {
	// MaxBoundaryAttempts 是合法正值时，随机内容几乎不可能耗尽重试；
	// 这里用 1 次重试确认该配置路径返回的是可判定的边界错误类型，
	// 而不是 panic 或其它错误（随机 192 位冲突概率可忽略）。
	data := makeData(200)
	a := serve.New(serve.Config{MaxBoundaryAttempts: 1})
	err := a.Build("bytes=0-49,100-149", source.NewMemory(data))
	if err != nil && !errors.Is(err, serve.ErrBoundaryAttempts) {
		t.Fatalf("unexpected error: %v", err)
	}
}
