# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前仓库根包 `mileage` 实现常旅客里程账户：

- 定级里程与可兑换里程独立记账，支持保底、舱位折算与等级加成。
- 按账户开通时刻划分固定定级周期，支持周期内升级、周期末最多降一级和跨周期补登。
- 支持航段入账、退票原值扣回、欠账抵扣、奖励票兑换与取消。
- 支持单调时钟、非活跃冻结、显式解冻和统一拒绝次序。
- `NewService` 接收 `Config`，公开 `CreateAccount`、`Credit`、`Refund`、`Redeem`、`CancelRedemption`、`Unfreeze`、`View`。
- 错误使用根包导出的哨兵错误，例如 `ErrClockRewind`、`ErrAccountFrozen`、`ErrInsufficientMiles`。
- 设计取舍和性能论证见 `DESIGN.md`。

如果 HOME 下的 Go 缓存只读，可将缓存指到 `/tmp`：

```bash
GOCACHE=/tmp/go-cache PATH=/usr/local/go/bin:$PATH go test ./...
GOCACHE=/tmp/go-cache PATH=/usr/local/go/bin:$PATH go test -race -v ./...
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
