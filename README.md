# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `carpool/`：沿固定走廊运行的网约车合乘匹配与费用分摊服务。
  覆盖在途并入的停靠延误/座位/上车时刻判定、按共享里程的分摊（取整差额归下单最早者）、
  锁价保护、中途取消重定价、等待队列与自动失效；所有操作可并发调用且结果可精确复现。
  设计取舍见 [DESIGN.md](DESIGN.md)。

```go
s, _ := carpool.NewService(carpool.Config{
    StopDuration: 10, TimePerDistance: 1, UnitPrice: 1,
    CancelFee: 5, MaxActiveOrders: 4,
})
s.AddVehicle("v1", 4, 0, 0)
res, _ := s.SubmitOrder("o1", 0, 100, 1, 20, 1000, 1) // res.Status: Matched/Onboard/Waiting
events, _ := s.UpdateVehiclePosition("v1", 100, 200)  // 上下车/结算/等待匹配/失效事件
view, _ := s.QueryOrder("o1")                         // 预计应付、锁价上限、状态
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
