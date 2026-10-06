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

## 常旅客里程账户（`ontology` 包）

`ontology/` 实现里程累积、定级与兑换账户系统。设计见 `ontology/DESIGN.md`。

```go
cfg := ffm.Config{
    RetroWindow: 30, MinMiles: 500, InactiveDuration: 100, CancelFee: 10,
    PeriodLength: 100, Thresholds: [3]int64{1000, 2000, 4000},
    Bonuses: [4]int64{0, 10, 20, 50},
}
sys, _ := ffm.NewSystem(cfg)
_ = sys.OpenAccount("a", 0)
r, err := sys.Post("a", 10, ffm.Segment{ID: "s1", Distance: 1000, Rate: 100, FlightTime: 10})
snap, _ := sys.Query("a", 10)
_ = sys.Redeem("a", "ticket-1", 20, 500)
_, _ = sys.CancelRedeem("a", "ticket-1", 25)
_ = sys.Refund("a", "s1", 30)
_ = sys.Unfreeze("a", 200)
```

错误统一为 `*ffm.Error`，`Code()` 返回 `ffm.ErrCode`，次序即拒绝优先级：

`参数非法 > 时钟回退 > 账户不存在 > 账户冻结 > 重复入账 > 补登超期 >
兑换记录不存在 > 兑换记录已取消 > 里程不足`。

随机差分测试会打印种子，用环境变量复现：

```bash
DIFF_SEED=12345 go test -run TestRandomDifferential -v ./ontology/
```
