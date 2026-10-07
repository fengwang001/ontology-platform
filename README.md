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

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 假设性历史动作预检子系统（hypcheck）

`hypcheck/` 提供对动作（Action）的只读假设性重新预检：给定历史时刻与假设调用，
按当时生效的校验钩子版本与权限继承快照重新演算前置/后置校验，给出允许/拒绝与
状态改变意图，且不改任何状态。

- 设计说明（关键取舍、被放弃方案、边界规定、成本证明、本地验证）：`hypcheck/DESIGN.md`
- 独立朴素重演对照模型：`hypcheck.NaiveReplay`
- 演示：`go run ./cmd/demo`
- 成本验证：`go test -run TestProbeCostSublinear -v ./hypcheck/hypchecktest`
- 随机差分：`go test -run TestRandomDifferential ./hypcheck/hypchecktest`

关键规定：版本边界左闭右开、边界时刻取新版本一侧；错误优先级 `E1>E2>E3>E4`；
前置失败阻止后置演算且不跨阶段聚合；as-of 快照基于不可变 AVL + 二分，
单次预检的版本探测次数为有界常数（`PrecheckResult.Probes`），不随历史总量线性增长。
