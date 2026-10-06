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

## 顺路合并派单（dispatch 包）

`dispatch` 实现即时配送平台的骑手顺路合并派单：新订单在满足全部在途订单
时效约束的前提下插入骑手取送路线，支持推定到达、停靠完成、订单取消与
并发派单。设计取舍见 [DESIGN.md](DESIGN.md)。

```go
src := dispatch.TravelTimeFunc(func(a, b dispatch.Point) int64 { /* 秒 */ })
s := dispatch.New(dispatch.Config{MaxDetour: 300}, src)
s.AddRider(dispatch.Rider{ID: "r1", Region: "g0", Pos: "H", DepartAt: 0, Capacity: 2}, 0)
s.CreateOrder(dispatch.Order{ID: "o1", Region: "g0", Pickup: "P", Drop: "D", ReadyAt: 10, PromiseAt: 600}, 1)
asg, err := s.DispatchOrder("o1", 2)
```

```bash
# 单元测试 + 朴素模型随机对拍（含竞态检测）
go test ./dispatch -race -v
```
