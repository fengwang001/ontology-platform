# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 货位分配系统（`slotting`）

为到货托盘在承重、净高、品类、混批与相邻隔离共同限制下选定货位，并在货位冻结、
托盘移库与批量上架时保持分配确定、占用账目一致。

- 设计说明（关键取舍、被放弃方案、复杂度论证、本地验证）：[`slotting/DESIGN.md`](slotting/DESIGN.md)
- 运行日志演示：`go run ./cmd/demo`
- 核心模块：`slotting/model.go`、`errors.go`、`constraints.go`、`treap.go`、`store.go`、`service.go`、`logging.go`
- 测试：需求点单测、拒绝优先级、朴素模型随机差分（60 种子×200 操作）、
  考察货位数复杂度验证、确定性重放、`-race` 并发不变量校验。

对外 API：

- `Service.AutoPutaway` / `PutawayTo` / `Move` / `Retrieve`
- `Service.Freeze` / `Unfreeze`
- `Service.BatchPutaway`（全有或全无，返回最小失败下标）
- `Service.Location` / `PalletLocation` / `ProductLocations`（只读一致快照）
- 拒绝原因通过 `errors.Is(err, slotting.ErrFrozen)` 或 `slotting.AsReason(err)` 区分；
  批量错误为 `*slotting.BatchError`。

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
