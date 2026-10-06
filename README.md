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

## 带宽采样计费结算器（`billing` 包）

按带宽采样计费的单周期结算器：支持乱序/重复/更正采样、按版本覆盖与
撤回、丢弃最高 5% 槽位后的计费速率查询、缺失容忍结算与封存重复返回。

```go
b, _ := billing.NewBiller(startUnix, nSlots) // nSlots ∈ [1, 1_000_000]
err := b.Submit(billing.Sample{Slot: 3, Inbound: 8e9, Outbound: 5e9, Version: 7})
rate, err := b.CurrentRate()                // 随时查询，期望 O(log K)
res, err := b.Settle(committedRate, 100)    // 容忍 1%（万分之一百）
res2, _ := b.Settle(otherRate, 0)           // res2.Repeated == true，内容同首次
o := b.Overview()                            // 收到数/缺失数/速率/是否已结算
```

错误以 `billing.ErrorCode` 区分（如 `billing.ErrVersionConflict`），
判定优先级：参数非法 > 已结算 > 版本冲突/过期/版本不符 > 不存在 >
无采样/数据不足；被拒操作不改变状态。

验证：

```bash
go test -race ./billing                     # 全量（含 1200 组随机差分对照）
go test ./billing -run TestRandomDifferential -v   # 逐步日志：/tmp/billing-differential.log
go test ./billing -run '^$' -bench BenchmarkQuery   # O(log K) vs O(K log K) 对照
```

设计取舍、被放弃方案与验证细节见 `billing/DESIGN.md`。
