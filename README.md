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

## 配送围栏（fence 包）

商家配送范围按环距分层，平台事件与商家等级共同施加动态收缩：

```go
import "ontology/fence"

s := fence.NewSystem()
// 商家 m 属于区域 r，基础范围为环距 0..3 的网格单元
s.RegisterMerchant(0, "m", "r", map[fence.Cell]int{
    {X: 0}: 0, {X: 1}: 1, {X: 2}: 2, {X: 3}: 3,
})
s.AddPlatformEvent(1, "storm", "r", 2, 10, 2) // 区域 r 在 [2,10) 收缩等级 2
rc, err := s.QueryReachable(5, "m", fence.Cell{X: 3})
// rc == fence.ShrunkTemporarily（环距 3 > 有效半径 1）
```

- 可达性：`QueryReachable` 返回 `Reachable` / `OutsideForever` / `ShrunkTemporarily`。
- 恢复时刻：`QueryNextRecovery` 返回当前可达、计划恢复时刻或无恢复（商家等级自身阻断）。
- 订单：`PlaceOrder` → `AcceptOrder`（收缩则拒绝并自动转为因收缩取消）→ `DeliverOrder`，
  已接单后不受收缩影响；`RerouteOrder` 允许一次改址。
- 错误分类：用 `fence.Code(err)` 判断非法参数、时钟回退、对象不存在、状态错误、
  永久不可达、暂时不可达等。

```bash
go test ./fence
go test -race -v -run TestDifferentialRandom ./fence   # 朴素模型差分与逐步日志
```

详见 `fence/DESIGN.md`。

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
