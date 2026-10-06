# 分区停车场预约服务

这是一个无外部依赖的 Go 包，提供预约、候补、核销、超时改派、取消、离场、费用和任意时刻车位归属查询。

## 模型入口

- `NewLot`：创建停车场。
- `AddZone`：添加分区、费率、提前量、宽限量和车位列表。
- `Reserve`：在有具体车位时立即预约。
- `RegisterWaitlist`：登记候补；若当下已有匹配车位会立即转预约。
- `CheckIn`：窗口内核销；遇超时占位时按规则改派。
- `Cancel`、`Depart`：未核销取消与已核销离场。
- `SpotOwner`：查询某分区某车位在某一秒的归属者。
- `Fee`：查询预约费用明细。

时间使用非负整数秒，预约区间为左闭右开；编号小于 10 的示例测试车位是普通位，编号大于等于 10 的是充电位。

## 本地验证

当前环境 Go 位于 `/usr/local/go/bin/go`，且默认构建缓存不可写，可用：

```bash
GOCACHE=/tmp/go-cache /usr/local/go/bin/go test ./...
GOCACHE=/tmp/go-cache /usr/local/go/bin/go test -race ./...
GOCACHE=/tmp/go-cache /usr/local/go/bin/go vet ./...
/usr/local/go/bin/gofmt -w *.go
```

若本机已正确配置 `PATH` 与可写的 `GOCACHE`，可直接运行：

```bash
go test ./...
go test -race ./...
go vet ./...
```

## 测试与可复现性

- `boundary_test.go` 覆盖题目列出的首尾相接、窗口端点、同刻失效候补、超时取整、改派、候补跳过和重复核销。
- `differential_test.go` 内置独立逐秒朴素模型，随机 30 个种子对照状态、费用和秒级归属。
- 差分测试使用 `go test -v -run TestRandomDifferentialModel` 可打印每条操作的输入、输出及判定依据。
- 设计取舍和复杂度证明见 `DESIGN.md`。
