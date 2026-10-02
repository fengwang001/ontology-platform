# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 容量预订簿（`booking` 包）

带历史缺席率自适应超卖与到场挤出补偿的容量预订簿，所有操作与查询可并发调用，
结果等价于某个串行顺序；相同操作序列重放得到完全相同的准入、挤出与补偿。

### 构造参数

`booking.New(C, W, Omax, R)`：每时段容量 `C`（1~10^6）、窗口长度 `W`（1~64）、
超卖系数上限 `Omax`（0~10000，万分比）、补偿单价 `R`（0~10^6）。
任一参数越界即以 `ErrInvalidConfig` 整体拒绝。

### 超卖系数与预订上限

- 状态含已结算的最大时段号 `lastSettled`（初值 -1）、最近 `W` 次 Settle 的
  `(预订量, 到场量)` 记录（不足 `W` 次时有多少用多少）、全局预订序号、
  每个租户的被挤次数 `k`（初值 0）。
- 当前超卖系数：设窗口内预订量总和为 `ΣR`、到场量总和为 `ΣA`，`ΣR` 为 0 时
  `O=0`，否则 `O = min(Omax, floor((ΣR−ΣA)×10000/ΣR))`（向下取整）。
- 时段预订上限为 `floor(C×(10000+O)/10000)`，`O` 取调用 `Book` 时刻的当前值；
  `O` 的变化不追溯已有预订。
- `Book(id, owner, slot, size, tier)`：`id`/`owner`/`slot` 为 0~10^6，
  `size` 为 1~10^6，`tier` 为 0~2（0 优先级最高）。该时段已订总量加 `size`
  不超过上限即准入（恰等于上限通过）。拒绝原因按顺序只报第一个：
  `ErrInvalidArgument`（参数非法）→ `ErrDuplicateID`（id 重复）→
  `ErrSlotSettled`（slot 不大于 lastSettled）→ `ErrOverLimit`（超出上限）。
  被拒绝的 Book 不改变任何状态，也不消耗序号。

### 结算、挤出与补偿

`Settle(slot, arrivals)`：`arrivals` 为 `(id, a)` 列表，`id` 必须属于该时段已有的
预订，`a` 为 0 到该预订 `size` 的整数，未列出的预订到场量为 0，id 重复视为参数非法。
`slot` 必须大于 `lastSettled`，中间未结算的时段视为从未发生（不进入窗口）。
拒绝原因按顺序只报第一个：`ErrInvalidArgument` → `ErrSlotRollback`。
被拒绝的 Settle 不改变任何状态。

令 `R0` 为该时段预订量总和、`A` 为到场量总和，结算依次：

1. 把 `(R0, A)` 追加进窗口（超过 `W` 条丢弃最旧；没有预订的时段也追加 `(0,0)`）。
2. 若 `A > C`，令 `excess = A−C`，把到场量大于 0 的预订按 **tier 降序**
   （数值大者先挤）、**预订序号降序**（后预订者先挤）排序，依次挤出
   `t = min(该预订到场量, 剩余 excess)` 个单位直到 `excess` 用完。
   每个被挤出预订的补偿为 `t×R×(1+min(k_owner, 3))`，
   其中 `k_owner` 取本次 Settle 开始前的值；被挤出的单位仍计入到场量 `A`。
3. 本次 Settle 中被挤出至少一个单位的每个租户 `k` 加一（每次 Settle 至多加一）。
4. `lastSettled = slot`。

`Settle` 返回每个预订的实到服务量与被挤出量及总补偿；每个已结算时段实到服务量
总和不超过 `C`，被挤出量总和恰等于 `max(0, A−C)`。

### 查询

`CurrentO`、`Limit`、`Booked(slot)`、`K(owner)`、`LastSettled`、`WindowLen`、`Seq`
均可并发调用。

## 环境要求

- Go 1.26+（`go version` 确认）

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./booking
go test -run TestWorkedExample ./booking

# 随机序列对照朴素模拟（日志含每步输入、输出与判定依据）
go test -v -run TestRandomAgainstModel ./booking

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
