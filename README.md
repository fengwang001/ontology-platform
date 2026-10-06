# ontology-platform

本仓库当前提供 `transfer` 包，用于管理仓库之间的库存调拨：创建冻结、发出、分批收货、等待关闭、短缺/超收登记与关闭后找回。

## 安装与导入

要求 Go 1.26+。

```go
import "ontology/transfer"
```

初始化系统：

```go
system := transfer.NewSystem(
    map[transfer.Warehouse]map[transfer.Product]transfer.Quantity{
        "wh-a": {"sku-1": 100},
        "wh-b": {"sku-1": 0},
    },
    transfer.Config{TolerancePerMille: 15, WaitDuration: 86400},
)
```

主要 API：

- `CreateOrder(id, source, destination, lines, at)`
- `CancelOrder(id, at)`
- `ShipOrder(id, at)`
- `ReceiveLine(id, lineIndex, quantity, at)`
- `CloseOrder(id, at)`
- `RecoverShortage(id, lineIndex, quantity, at)`
- `Stock(warehouse, product)`
- `Order(id)`
- `VerifyConservation()`

错误可用 `errors.As` 解析为 `transfer.Failure`，通过 `Kind` 判断错误类别；库存不足时读取 `LineIndex`。

## 测试

```bash
go test ./...
go test -race ./...
go test -v ./transfer
```

测试覆盖容忍额向下取整与恰等于边界、等待时长边界、提前关闭、短缺与超收并存、找回边界、取消释放、批量创建回滚、高发生命周期、并发快照以及 40 个固定种子的随机朴素模型差分对照。

## 文档

详细设计、复杂度证明、方案取舍和审计日志说明见：

- `docs/transfer-design.md`

## 代码检查

```bash
gofmt -d .
go vet ./...
```
