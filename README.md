# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 预约单排程系统（`scheduler/`）

即时配送预约单排程：时段名额账本、预约生命周期与改期、释放派单衔接、并发协调。
设计取舍见 [DESIGN.md](DESIGN.md)。

```go
sys, _ := scheduler.NewSystem(scheduler.Config{
	DispatchLead: 30, CutoffLead: 60, EarliestLead: 100, LatestLead: 2000, MaxShiftSpan: 300,
})
sys.AddRegion(0, "R")
sys.AddSlot(0, "R", 1000, 1300, 8)          // 区域 R 增加时段 [1000,1300)，名额 8
res, _ := sys.Place(0, "order-1", "R", 1000, true) // 下单，接受顺延
sys.Reschedule(100, "order-1", "R", 1000)   // 改期（截止前）
sys.Cancel(970, "order-1")                  // 取消（释放后取消会记录标志）
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
