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

## 配送路线时间窗监控（`routewatch`）

`routewatch` 包实现配送车辆路线执行中的时间窗监控：

- 按计划逐站推演到达/开始服务/离开时刻，处理早到等待、恰在右端点准时、
  软窗迟到照服、硬窗迟到跳过（跳过后行驶时长取上一被服务站直达值）。
- 连续驾驶上限（恰等于上限允许、超一须先休息）与站点停留达休息时长清零
  （恰等于也算）。
- 实际上报（必须严格晚于前一实际上报站离开）与站点取消，只重推受影响后缀，
  前缀推演与发布值不变。
- 对外发布 ETA：首次发布取推演值；之后仅当差值严格大于防抖阈值（恰等不
  更新）且距时钟严格大于锁定窗口（恰等于锁定窗口即冻结）才更新；到达、
  跳过、取消后撤下发布值。
- 拒绝优先级：参数非法 → 时钟回退 → 站点不存在 → 状态不符 → 已跳过 →
  顺序错误；被拒绝操作不改任何状态。

快速上手：

```go
m, _ := routewatch.New(cfg)
sn, err := m.ReportArrival("S3", actualArrivalSeconds, opTimeSeconds)
sn, err = m.CancelStop("S4", opTimeSeconds)
```

测试（400 组随机操作序列与独立朴素模型 `routewatch/naive` 全量对照）：

```bash
go test -race ./...
go test -run=XXX -bench=Resimulate ./routewatch   # 后缀复杂度证据
```

设计取舍、被放弃方案与验证细节见 `routewatch/DESIGN.md`。
