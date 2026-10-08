// Package constexpr 是静态语言常量表达式的结构化求值器。
//
// # 概念
//
// 值有四种种类：KindInt（任意精度整数）、KindRat（精确有理数）、
// KindBool、KindString（字节序比较）。字面量与未标注类型的命名常量是
// 无类型常量；带具体类型（i8..u64、f64、bool、string）的是有类型常量，
// 每一步运算结果都必须可表示于该类型，越界即错误，不回绕。
//
// 典型用法：
//
//	import "ontology/constexpr"
//
//	r := constexpr.NewRegistry()
//	_ = r.Register("width", constexpr.IntLit(big.NewInt(640)))
//
//	e := constexpr.Binary(constexpr.OpAdd,
//	    constexpr.Ref("width"),
//	    constexpr.Conv("i32", constexpr.IntLit(big.NewInt(-40))))
//	v, err := r.NewEvaluator().Eval(e)
//
// 无类型表达式可用包级 Eval：
//
//	v, err := constexpr.Eval(
//	    constexpr.Binary(constexpr.OpDiv,
//	        constexpr.IntLit(big.NewInt(7)),
//	        constexpr.RatLit(big.NewRat(2, 1))))
//	// v.Kind() == KindRat，v.Rat() == 7/2（精确除法，有理数种类）
//
// 错误通过 *EvalError 的 Code 字段区分：类型不匹配、非法操作、除零、
// 截断、越界、常量过大、未知名字、重复登记、参数非法各自可判别。
//
// 求值支持 WithTracer 选项，可记录每个节点的输入、输出与错误，用于
// 打印“每次输入/输出/判定依据”。
package constexpr
