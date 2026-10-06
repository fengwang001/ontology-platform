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

## 变压器容量预约系统（`pkg/reservation`）

纯 Go、无第三方依赖的容量预约内核，规则见 `pkg/reservation/DESIGN.md`。

```go
sys := reservation.NewSystem(
    map[int]int{1: 10, 2: 6},                         // 馈线 -> 固定上限
    []reservation.CapacityRecord{{EffectiveAt: 0, Capacity: 100}},
    reservation.Config{HoldDuration: 5},              // 占位时长
)
id, err := sys.Create(now, 1, reservation.Interval{Start: 0, End: 8}, 4) // 先占位
err = sys.Confirm(now, id)                    // 到期前确认；到期时刻本身算到期
err = sys.Reschedule(now, id, iv, power)      // 改约（排除自身、原子替换）
err = sys.Release(now, id)                    // 释放/提前结束
_, err = sys.ChangeCapacity(now, rec)         // 容量登记；已确认挡住下调，占位按创建晚->早取消
err = sys.Advance(newNow)                     // 只能前进；落定到期占位与已完成预约
```

- 错误用 `*reservation.OpError` 表示，`Kind` 为七类错误之一；容量不足时看 `CapInfo.Time/Level`。
- 注入 `Config.Log`（实现 `reservation.Logger`）可逐条打印输入、输出与判定依据。
- `NaiveSystem` 是逐时刻重写的独立参考模型；`TestRandomDifferential` 对大量随机操作序列做差分对照。
- 关键测试：`go test -race -v ./pkg/reservation/`。

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
