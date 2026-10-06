// Package apportion 实现同一损失下多张保单重复投保的分摊赔付引擎。
//
// 职责拆分为：保单/损失登记与校验（policy.go）、两阶段比例裁定（allocate.go）、
// 账目与并发控制（engine.go）、规则错误码（errors.go）。
//
// 基本用法：
//
//	e := apportion.NewEngine(logWriter) // nil 表示不打印裁定日志
//	err := e.Register(apportion.Policy{...})
//	out, err := e.Accept(apportion.Loss{...})
//	_, err = e.Undo(lossNo, insured)
//
// 规则细节、关键取舍与本地验证方法见同目录 DESIGN.md。
package apportion
