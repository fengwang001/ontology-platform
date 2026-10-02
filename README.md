# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 预算花费守卫

`budgetguard` 包提供可并发重放的周期预算守卫：

- 阶梯告警为一次性事件。评估时按百分比升序检查，首次满足 `S*100 >= B*p`（包含相等）才置位并产生 `LADDER(p)`；已置位的阶梯不会因退款而清除。
- 只有 `AdjustBudget(B2)` 会重新武装阶梯：在设置新预算前，清除所有已触发且不再满足 `S*100 >= B2*p` 的标志，然后用新预算重新评估；仍成立的标志保持不变。
- 已过天数为 `e=cur+1`。当 `e >= Dmin` 时，预测值为 `proj=floor(S*D/e)`，并在 `proj*100 >= B*F` 时产生一次 `FORECAST`；条件变假会清除预测标志，之后再次变真会重新告警。预测乘法使用无符号 128 位整数比较，避免 `proj*100` 溢出 int64。
- 冻结状态恒等于 `S >= B`。一次评估中事件顺序固定为所有新触发的 `LADDER`、至多一个 `FORECAST`、最后是冻结状态迁移；从非冻结变为冻结产生 `FROZE`，反之产生 `THAWED`，状态未变则无迁移事件。
- `Spend(day, x)` 的拒绝原因按优先级依次为参数非法、日期回退、已冻结且 `x>0`、结果花费小于 0 或大于 `10^15`。冻结时 `x<=0` 的零金额或退款仍会被接受；任何被拒绝操作都不改变状态。
- `New` 要求 `1<=D<=366`、`1<=k<=8`、阶梯严格递增且每项在 `1..1000`、`1<=F<=1000`、`1<=Dmin<=D`、`1<=B<=10^15`；非法配置整体拒绝。
- 所有公开操作均由互斥保护，事件只返回本次调用新产生的列表；`Snapshot` 可用于精确复现 `S`、`cur`、预算、阶梯标志、预测标志和冻结状态。

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

# 预算守卫测试（含 2000 组 math/big.Int 朴素模型随机对照与逐步判定日志）
go test -race -v ./budgetguard

# 若默认 Go 构建缓存目录只读，可指定可写缓存
GOCACHE=/tmp/go-cache go test -race ./...

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
