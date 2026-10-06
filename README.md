# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

### `signal` — 城市干线路口信号配时控制与绿波协调

- 固定相序循环的配时方案：每相位最小/最大绿与清空时长，方案在循环结束瞬间生效，
  未生效方案被后接受者静默替换（`PendingPlan` 可查）。
- 两级优先：紧急车辆（延长当前相位至最大绿，或跑满最小绿后跳相直达目标相位，
  保持至通过确认或最大绿；同一相位不得相邻两循环连续被跳过）与公交（仅延长/缩短
  当前相位，紧急优先时被抢占且终态可查为 `Preempted`）。
- 绿波回归：优先服务造成相位差偏离后，每循环按方案调整上限逐步缩短/延长绿时回归，
  半周期偏离取延长方向，回归中途被打断则按实际状态重新测定偏差。
- 任意时刻查询当前相位、已持续/剩余时长与相位差偏离；查询开销不随循环数增长
  （事件引擎 + O(1) 跨循环快进，测试以事件计数证明）。
- 并发安全（互斥锁串行化），克隆-校验-提交保证被拒绝操作零痕迹，重放完全确定。

```go
x, _ := signal.NewIntersection(specs, plan, 0)
_ = x.RequestEmergency("E1", 2, 10)   // id, 目标相位, 时刻
_ = x.ConfirmPassage("E1", 25)
_ = x.RequestBus("B1", signal.BusExtend, 5, 30)
_ = x.ChangePlan(newPlan, 40)          // 下一循环结束生效
q := x.Query(50)                       // 相位/已持续/剩余/偏差
s, _ := x.RequestState("B1", 50)       // Queued/Serving/Applied/Completed/Preempted
```

设计与取舍见 [signal/DESIGN.md](signal/DESIGN.md)。

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
