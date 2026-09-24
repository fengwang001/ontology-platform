// Package check 顺序执行的朴素参照，以及 pmap 的全部测试。
package check

import "context"

// Naive 顺序执行参照：逐个处理，首个失败处停止，返回已产出的结果与错误。
func Naive[In, Out any](ctx context.Context, inputs []In, fn func(context.Context, In) (Out, error)) ([]Out, error) {
	outs := make([]Out, 0, len(inputs))
	for _, in := range inputs {
		out, err := fn(ctx, in)
		if err != nil {
			return outs, err
		}
		outs = append(outs, out)
	}
	return outs, nil
}
