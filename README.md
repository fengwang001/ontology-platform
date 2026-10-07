# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 本模块动作校验机制（actionguard）
go test ./actionguard
go test -race -count=5 ./actionguard
go test -run=NONE -bench CheckConsistency -benchmem ./actionguard
go run ./cmd/demo

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## Action 前置/后置校验分离机制

动作（Action）执行拆分为职责严格分离的两个阶段，代码位于
`actionguard/`：

- 前置校验只读执行前已持久化的深拷贝快照；前置失败返回**全部**不通过
  条件，且不产生任何状态变化、不消耗版本号/序号、不入历史。
- 写入计划是纯函数产物，同对象多次写入折叠为最终值；全部计划生成后一次性
  投影出唯一最终快照供后置校验。后置失败只返回**决定性的一个**条件，
  计划整体放弃，仅在独立失败轨迹留痕。
- 声明自相矛盾（同阶段存在可合一且极性相反的字面量）在动作**注册期**
  即报 `DefinitionError`，检测复杂度 O(n²·k)，与历史调用次数无关。
- 每次调用在单个临界区内完成“快照→前置→计划→后置→提交”，并发调用
  的被接受集合与效果严格等价于某个全序串行执行。

完整取舍、被放弃方案与验证方法见 `docs/DESIGN.md`；端到端演示见
`cmd/demo`。
