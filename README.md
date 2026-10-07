# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：事件溯源对象状态重建

支持对象类型单继承（子类型继承父类型属性定义并可收窄覆盖取值范围）、
类型演变事件（实例在父子类型间往返迁移）、规则版本化（历史事件按当时
生效的继承/覆盖规则解释）、快照加速重建（开销不随演变次数线性增长）、
并发可线性化的追加与重建。

- 设计说明（关键取舍、被放弃的方案、验证方法）：[docs/DESIGN.md](docs/DESIGN.md)
- 核心包：根包 `ontology`（`types.go` / `rules.go` / `event.go` /
  `replay.go` / `store.go` / `naive.go`）
- 演示程序：`cmd/server`

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

# 重建成本基准（验证开销不随演变次数增长）
go test -bench=BenchmarkRebuild -benchmem .

# 差异测试审计日志落盘（默认写入测试临时目录）
ONTOLOGY_AUDIT_DIR=/tmp/audit go test -run 'TestExhaustive|TestRandom' -v ./...

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
