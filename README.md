# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 当前仓库的药品召回系统是 Go package
go test ./...
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 药品批次召回与追溯锁定

核心包位于仓库根目录，入口为 `NewSystem()`。公开操作包括：

- `Inbound`：入库登记，同一药品批号不能重复。
- `Transfer`：药库与病区药柜之间调拨。
- `Dispense`：向患者发放，三级召回需传 `InformedConsent: true`。
- `Return`：患者退药，任何召回等级下均允许，退回数量进入药库。
- `RegisterRecall` / `CancelRecall`：登记或解除批号闭区间召回。
- `RecoveryList`：查询一级或二级有效召回的 FIFO 追回清单。
- `QueryBatch`：查询各位置库存、有效等级和并列最严召回编号。

随机差分测试可通过 `go test -v -run TestRandomEquivalence ./...` 打印每步输入、输出、错误码和判定依据；复杂度取舍、基准结果与本地复现见 `DESIGN.md`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
