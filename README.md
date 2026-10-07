# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 功能

- 跨实例批量更新：每项变更以"目标实例当前必须恰好处于指定版本"为前置
  条件，批次内所有前置条件一次性联合判定，整批生效或整批拒绝。
- 四类互斥结果：批次内重复前置声明、前置版本不满足、基数约束不满足、
  成功提交；判定顺序为 重复声明 → 版本 → 基数。
- 拒绝路径不产生任何可观察的状态变化；每个实例的版本号按各自独立的
  严格单调序列推进。
- 每个批次的完整判定记录（前置条件、判定依据、结果）写入判定日志，
  支持按逻辑时刻重放核验。

设计取舍与被放弃的方案见 [docs/design.md](docs/design.md)。

## 代码结构

- `ontology/` — 核心包：`Store`（有序两阶段锁实现）、`Model`（朴素
  全局锁参照模型，用于差分测试）、类型与判定日志。
- `cmd/server/` — 命令行演示。

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
