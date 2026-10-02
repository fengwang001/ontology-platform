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

## 容量预订簿

根包提供 `CapacityBook`，入口为：

- `NewCapacityBook(C, W, Omax, R)`：构造容量、窗口、超卖上限、补偿单价；参数越界返回 `ErrInvalidConfig`。
- `Book(id, owner, slot, size, tier)`：登记一个预订，成功后分配全局递增预订序号。
- `Settle(slot, arrivals)`：结算一个时段，返回每个预订的到场、实到服务、挤出量与补偿。

### 超卖窗口

每次成功 `Settle` 都向最近窗口追加一条 `(R0, A)`，其中 `R0` 是该时段预订量总和，`A` 是到场量总和。窗口最多保留 `W` 条，超出后丢弃最旧记录；没有任何预订的时段也会追加 `(0,0)`，因此会占窗口位置并稀释后续缺席率。结算时段可以跳跃，未结算的中间时段不会进入窗口。

设窗口内总和为 `ΣR` 与 `ΣA`，当前超卖系数以万分比表示：

```text
ΣR = 0                         -> O = 0
ΣR > 0                         -> O = min(Omax, floor((ΣR-ΣA)*10000/ΣR))
当前 Book 上限                 -> floor(C*(10000+O)/10000)
```

上限只在每次 `Book` 调用时读取当时的 `O`；之后窗口变化导致 `O` 下降，不会追溯撤销或压缩已成功的预订。

### 准入顺序

`Book` 按以下顺序只返回第一个错误：

1. 参数非法：`id/owner/slot` 越界、`size` 越界、`tier` 不在 `0..2`。
2. `id` 重复。
3. `slot <= lastSettled`，返回 `ErrSlotSettled`。
4. 该时段已有预订量加 `size` 超过当前上限，返回 `ErrCapacityLimit`。

恰好等于上限允许通过。被拒绝的调用不改变任何状态，也不消耗全局预订序号。

`Settle` 按以下顺序只返回第一个错误：

1. 参数非法：`slot` 越界、到场量超出预订量、`id` 不属于该时段、同一 `id` 重复列出。
2. `slot <= lastSettled`，返回 `ErrSlotRollback`。

参数校验和回退判断在任何状态修改前完成，因此拒绝不会移动窗口或改变 `k`、`lastSettled`。

### 到场挤出与补偿

当 `A > C` 时，`excess = A-C`。只对到场量大于 0 的预订排序，规则为：

1. `tier` 降序：`tier=2` 最先挤出，`tier=0` 最后挤出。
2. 同 `tier` 按全局预订序号降序：后预订者先挤出。

每个预订挤出 `min(该预订到场量, 剩余 excess)`。被挤出单位仍计入到场量，但不计入实到服务量；因此实到服务总和不超过 `C`，挤出总量恰为 `max(0, A-C)`。

若租户 `owner` 在本次结算开始前已被挤出 `k` 次，某预订挤出 `t` 个单位的补偿为：

```text
t * R * (1 + min(k, 3))
```

同一租户在一次 `Settle` 中即使有多个预订被挤出，`k` 也只增加 1；这些预订的补偿统一使用本次结算开始前的旧 `k`。`k >= 3` 后倍数封顶为 4。

所有查询和写操作均由同一把读写互斥锁保护，行为等价于某个串行执行顺序；相同操作序列使用相同输入重放时，准入、挤出清单、序号和补偿完全一致。

### 本地验证

```bash
# 全量测试；随机对拍包含 2000 组固定种子序列，并在 -v 日志中打印输入、输出与判定依据
go test -v ./...

# 并发竞态检测
go test -race ./...

go vet ./...
gofmt -d .
```

测试覆盖精确上限、`Omax` 封顶、缺席率向下取整、空结算稀释窗口、旧记录滑出、超卖变化不追溯、跳时段、`tier` 与预订序号挤出顺序、部分挤出、同租户单次结算合并升级、`k>=3` 封顶、被挤出单位仍到场、拒绝原子性，以及与独立朴素模拟的 2000 组随机序列对拍。
