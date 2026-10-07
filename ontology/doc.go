// Package ontology 实现本体平台批量导入的校验钩子可见性子系统。
//
// 子系统由三个协作角色构成：
//   - Importer（importer.go）：批量导入分批执行；
//   - overlay / Snapshot / ScopedView（overlay.go, store.go）：
//     钩子触发与批内可见性解析；
//   - HookError 与 BatchResult.FirstError（errors.go, importer.go）：
//     错误归一化与固定优先级报告。
//
// 批内可见性严格遵循列表顺序：第 i 条记录的前置钩子只能看到已提交状态
// 与本批 [0,i) 中已通过前置钩子的记录效果。详见 DESIGN.md。
package ontology
