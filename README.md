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

## 区域运力调度与高峰加价（`surge` 包）

`surge/` 提供即时配送的区域运力调度与高峰加价系统，包含：

- 供需账本（上线/下线/跨区、持单满额自动移出可用运力）
- 档位状态机（上调即时生效、跨多档；下调需连续确认且每次只降一档）
- 订单创建即锁定档位、骑手按完成订单的锁定档结算补贴（迟到豁免）
- 全局单调时钟、类型化错误码、单一互斥锁保证并发等价串行
- 独立的朴素全量重算模型 `NaiveModel` 用于差分对照

构造示例：

```go
sys, err := surge.NewSystem(surge.Config{
    Thresholds:        []float64{1.0, 2.0, 4.0}, // 严格递增的触发比率
    DownConfirmations: 2,                        // 下调连续确认次数
    MaxHeld:           2,                        // 骑手同时持单上限
    Subsidies:         []int64{0, 10, 20, 40},   // 每档补贴额
    MinEvalInterval:   5,                        // 评估最小间隔（秒，含等号）
})
```

验证：

```bash
go test ./surge -race                       # 规格 + 并发 + 差分
go test ./surge -run TestDifferential -v    # 逐步日志（同时写入 /tmp/surge-diff.log）
SURGE_DIFF_LOG=1 go test ./surge -run TestDifferentialRandomized -v
go test ./surge -run '^$' -bench=.          # O(1) 复杂度基准
```

详见 `surge/DESIGN.md`。
