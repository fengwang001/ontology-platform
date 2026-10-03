# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 利他锁（Altruistic Locking）

`altruistic` 包实现利他锁管理器（`altruistic.New` / `Manager`）。长事务可以把
自己已经用完的对象提前**捐赠（Donate）**出去：捐赠后该对象立即无持有者，其他
事务可马上加锁，从而缩短临界区；代价是捐赠者通过**尾流（wake，`wk`）**关系
约束所有接触过其捐赠对象的事务。

### 数据结构

- 对象数 `N`（1–64，编号 `0..N-1`），每个对象至多一个持有者。
- 事务号由 `Begin()` 从 1 起递增分配，事务处于活跃 / 已结束 / 已中止三态之一。
- 每个事务维护：
  - `locked`：它**曾经加锁成功过**的全部对象（含现持有与已捐赠，只增）。
  - 现持有集合（持有集合两两不交）。
  - `donated`：它捐赠过的全部对象（只增）。
  - `wk(T)`：尾流集合，记录“T 在某对象已属 `donated(U)` 的那一刻成功加锁该
    对象”的各捐赠事务 U（记于加锁成功时；先于 U 捐赠而加锁的事务**不追溯**入
    尾流）。

### 捐赠与尾流规则

- `Donate(t,o)`：`o` 须由 t 持有；之后 `o` 无持有者并入 `donated(t)`，t 仍
  活跃且可继续加锁新对象。Donate 不做任何尾流判定。
- `Lock(t,o)` 成功时，若有其他活跃事务 U 满足 `o ∈ donated(U)`，则把 U 并入
  `wk(t)`；随后任意时刻对活跃 t 与 `wk(t)` 中每个活跃 U 都保持
  `locked(t) ⊆ donated(U)`。
- 约束方事务结束或中止后即不再活跃，其尾流约束随之消失。

### `Lock` 判定顺序（只报第一个失败）

1. `o ∈ donated(t)`：`already_donated`（不可重新加锁自己捐出的对象）。
2. `o` 的持有者是 t：成功且无变化。
3. `o` 被他人持有：`object_held`（**占用判定先于尾流判定**）。
4. 令 `S' = locked(t) ∪ {o}`，`C = {U ∈ wk(t) : U 活跃} ∪ {U ≠ t : U 活跃 且 o ∈ donated(U)}`；
   逐个活跃事务做位集判断，只要存在 `S' ⊄ donated(U)` 即以 `wake_violation`
   拒绝，并报事务号最小的 U（恰为子集则放行，多一个对象即违规）。
5. 否则授予：t 成为持有者，`locked(t) ← S'`，捐赠过 `o` 的其他活跃事务并入
   `wk(t)`。

### `Finish` 与级联中止 `Abort`

- `Finish(t)`：若 `wk(t)` 中仍有活跃事务，以 `wake_unfinished` 拒绝并报其中
  事务号最小者；否则释放全部持有并转已结束。
- `Abort(t)`：令 `A = {t}`，反复并入满足“`wk(U)` 含 A 中某事务”的活跃 U，
  直到闭包不再增长：

  `A = {t}`，`A ← A ∪ { U 活跃 : wk(U) ∩ A ≠ ∅ }`（不动点）。

  A 中事务全部转已中止并释放持有，返回 A 的升序列表。Abort 不做尾流判定，只
  作用于活跃事务，且不波及未接触过捐赠对象的事务（例如独立事务不在任何
  `wk` 关系中）。

### 拒绝原因的总判定顺序

所有调用先做公共前置校验，再做各调用的专有判定，任一拒绝都不改变状态：

1. 事务号不存在：`unknown_transaction`；
2. 事务非活跃：`not_active`；
3. 对象越界（仅 `Lock`、`Donate`）：`object_out_of_range`；
4. 调用专有原因（见上，`New` 的 `N ∉ [1,64]` 为 `invalid_n`，构造即整体拒绝）。

### 并发与可复现性

全部方法在同一互斥锁下串行化，并发调用等价于某个串行顺序；对象集合用
`uint64` 位集运算，尾流判定只遍历候选的活跃事务，检查事务数不超过活跃事务数。
相同调用序列重放得到完全相同的结果与事务号。

### 本地验证

```bash
# 全量测试（含 2000 组随机序列与朴素模型差分对照、并发可串行化）
go test ./altruistic/

# 竞态检测 + 详细日志（输入、输出、判定依据）
go test -race -v ./altruistic/

# 只跑 2000 组差分测试
go test -run TestDifferentialAgainstNaive -v ./altruistic/
```

`altruistic/naive_test.go` 内的 `naiveSim` 是严格按上述规则逐步写成的朴素
参考实现（map 集合、无位集优化）；`diff_test.go` 随机生成 Begin/Lock/Donate/
Finish/Abort 序列，逐调用比对返回值（拒绝原因、最小事务号、Abort 升序列表）
与完整内部状态（state/locked/holding/donated/wk/holder），失败时打印全部
输入、输出与判定依据。

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
