# hypcheck：假设性历史动作预检

只读重演算某动作在历史时刻 `at` 会经历的校验，依据当时的钩子版本与权限继承快照。

- 设计、取舍、被放弃方案与验证方法：见 `DESIGN.md`
- 入口：`Engine.Precheck`
- 独立对照：`NaiveReplay.Precheck`
- 审计：`MemoryAuditor`（SHA-256 哈希链）
- 演示：`go run ./cmd/demo`
- 测试：`../hypcheck/hypchecktest`

最小示例见 `../cmd/demo/main.go`。
