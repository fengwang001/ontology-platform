# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## microgrid 包

`microgrid/` 实现微电网储能调度控制器：荷电状态管理、充放电限制、关键负荷备用、
并网/孤岛模式、时隙计划的接受与后缀撤销、执行偏差重核验、累计吞吐维护锁定。
设计取舍见 [DESIGN.md](DESIGN.md)。

```go
cfg := microgrid.Config{Capacity: 100, MinSoC: 10, MaxSoC: 90, /* ... */ InitialSoC: 50}
c, _ := microgrid.NewController(cfg)
c.UpdateForecast(1, []int{3, 1, 4})                    // 登记关键负荷预测
c.SubmitPlan(1, []microgrid.PlanAction{                // 提交连续时隙计划
	{Action: microgrid.ActionDischarge, Amount: 10},
})
c.RecordActual(0, microgrid.ActionIdle, 0)             // 登记当前时隙实际值并推进
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
