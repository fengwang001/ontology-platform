# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

本仓库实现跨实例**联合版本前置条件批量更新**：一个批次内每项变更都以“目标实例
当前必须恰好处于指定版本”为前置条件，所有前置条件在同一逻辑时刻联合判定通过后
整批才原子生效；任一不满足则整批拒绝，不留任何状态痕迹。四类互斥结果按
`duplicate_precondition → version_mismatch → cardinality_violation → committed`
顺序判定。并发控制采用按实例 ID 全局排序的严格两阶段锁，并以独立的朴素全局锁
模型做随机差分对照。

详见 `docs/DESIGN.md`（关键取舍、放弃的方案、开销证据、本地验证）。

## 核心用法

```go
store := ontology.New(cfg)
store.CreateInstance("alice", "Person")

res := store.Commit(ontology.Batch{
    ID: "b1",
    Preconditions: []ontology.Precondition{
        {Instance: "alice", ExpectedVersion: 1},
    },
    Ops: []ontology.Op{
        {Kind: ontology.OpSetAttr, Instance: "alice", Attr: "name", Value: "Alice"},
    },
})
// res.Status: committed / version_mismatch / cardinality_violation /
//             duplicate_precondition
// res.Observed 为联合判定时读到的真实版本证据
// store.Journal() 为可重放的追加式判定日志（NDJSON）
```

日志重放核验：

```bash
go run ./cmd/demo -out /tmp/decisions.ndjson
go run ./cmd/replay -journal /tmp/decisions.ndjson
```

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
