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

## inline — 编译器内联决策子系统

包 `inline/` 在固定预算规则下，对一组函数及其调用关系给出确定性的内联决策。

- `registry.go`：函数登记、输入校验、任务期冻结、不可变快照。
- `budget.go`：每个根函数的尺寸账本，预算判定 O(1)，与全程序函数总数无关。
- `recursion.go`：当前展开路径；直接递归与链上出现次数统计只触及本路径。
- `decision.go`：按热度（其次按路径）逐点考察、固定优先级原因判定、热度传播、日志。
- `report.go`：不可变报告（每调用点去向、初始/最终尺寸、最深展开链）。
- `decision_test.go` / `naive_test.go`：边界、连锁、互递归与兄弟隔离、插序、
  原因优先级、并发隔离、冻结策略、日志，以及独立朴素模型对 400 组随机图的逐项对照。

快速验证：

```bash
go test ./inline/
go test -race ./inline/
```

关键取舍、被放弃方案与可验证性质见 `inline/DESIGN.md`。
