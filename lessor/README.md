# lessor：etcd 式租约管理器

`lessor` 包实现一个支持主从切换的租约（lease）管理器：授予带 TTL 的租约、
续约、把键挂靠到租约、在到期时按限速撤销，并通过检查点（checkpoint）让
主从切换后剩余寿命、待撤销积压与被撤销的键集合可精确复现。

## 构造参数与角色

`New(Config{MinTTL, MaxTTL, E, R, Kmax})`，任何参数越界都整体拒绝
（返回 `ErrInvalidConfig`）：

| 参数 | 含义 | 合法范围 |
| --- | --- | --- |
| `MinTTL` | 最小有效 TTL | `1 … 10^6` |
| `MaxTTL` | 最大申请 TTL | `MinTTL … 10^9` |
| `E` | 主从切换宽限 | `0 … 10^9` |
| `R` | 每次 Tick 的撤销上限 | `1 … 10^6` |
| `Kmax` | 每租约挂靠键上限 | `1 … 10^6` |

新引擎初始为**从（follower）**；时钟水位 `T` 初值为 0。`Demote(now)` 主→从、
`Promote(now)` 从→主，错误角色下调用返回 `ErrWrongRole`。

所有带 `now` 的操作要求 `0 ≤ now ≤ 10^15` 且 `now ≥ T`，成功后 `T = now`
（只读的 `TTL` 同样校验时间，但不推进 `T`）。

## 有效 TTL 与到期判定

- `Grant(id, ttl, now)`（仅主）要求 `1 ≤ ttl ≤ MaxTTL`，有效 TTL
  **`g = max(ttl, MinTTL)`**，到期时刻 **`x = now + g`**。
- `Renew(id, now)`（仅主）在 **`x ≤ now` 即已过期**（`x == now` 也算过期），
  返回 `ErrExpired`，租约仍留在积压中；成功时 `x = now + g`、`sv = 0`，
  返回值是有效 TTL `g`（不是当初申请的 ttl）。
- `Attach(key, id, now)` 要求租约存在且 `x > now`，否则分别返回
  `ErrNoLease` / `ErrExpired`。
- 主态 `TTL(id, now) = max(x-now, 0)`；从态返回 `sv > 0 ? sv : g`。

## Tick 的撤销次序与限速积压

`Tick(now)`（仅主）取出所有 `x ≤ now` 的租约，按 **`(x, id)` 升序**撤销，
每次最多撤销 `R` 个：

- 被撤销的租约被删除，其挂靠键一并脱钩并按**升序**随结果返回
  `[]Revocation{{ID, Keys}}`；
- 排不上的到期租约作为**积压**留下，下一次 Tick 继续；积压中 `x` 更早的
  租约永远先于后续到期的租约被撤销；
- `Revoke(id, now)` 无视到期立即撤销一个存在的租约，**不占用 `R`**。

到期集合用最小堆维护。`Renew`（改 `x`）、`Revoke`（删除）、`Promote`
（整体重建）发生后堆立即与之完全一致——不保留过期堆项靠惰性跳过。
非导出计数器 `tickPeeks`（经 `TickPeeks()` 读取）记录 Tick 检视堆顶的次数：
**每次 Tick 检视堆顶数 ≤ 本次撤销数 + 1**（最后那个未到期堆顶是唯一的
“+1”）。测试在 100 与 10000 租约两档（绝大多数未到期）下验证该值相同（均为 4）。

## 检查点与 Promote 的到期推导

- `Checkpoint(now)`（仅主）只写**未到期**租约：对 `x > now` 的租约令
  `sv = x - now`；其余（含积压）不变。
- `Demote(now)` 只切角色，不修改任何租约数据。
- `Promote(now)` 对**每个**租约（含积压中的）重新赋予到期时刻：

```
x = now + E + (sv > 0 ? sv : g)
```

`Promote` 不清 `sv`；`Renew` 成功会清 `sv`，因此「续约后再切换」得到完整
`g`，而「检查点后切换」得到检查点剩余寿命加切换宽限。

## 键挂靠规则

- 每个非空键至多挂在一个租约上；内部以全局 `keyOwner` 映射保证。
- `Attach` 到已挂靠的同一租约是幂等无操作（成功）。
- 目标租约已满 `Kmax` 时返回 `ErrLeaseFull`，且**先判容量、后摘除**：
  键不会从原租约上掉下来。
- 容量允许时，键先从原租约摘除再挂入新租约；租约被撤销（Tick/Revoke）
  后其键全部脱钩。

## 错误判定顺序

每个操作按以下顺序判定，被拒绝时不改任何租约、键挂靠、`T` 与积压：

1. 参数非法：`id < 1`、`key == ""`、`ttl` 越界；
2. 时间非法：`now` 越界或 `now < T`；
3. 角色错误：从态调用主操作，或 `Promote`/`Demote` 角色不符；
4. 各操作自身语义：Grant 的租约已存在；Renew 的不存在/已过期；Attach 的
   不存在/已过期/已满；Revoke 与 TTL 的不存在。

所有操作在同一把互斥锁下串行化，并发调用结果等价于某个串行顺序。

## 本地验证

```bash
# 全量测试（需要 Go 1.26+；若默认 GOCACHE 只读，可重定向缓存目录）
GOCACHE=/tmp/gocache go test ./...

# 竞态检测
GOCACHE=/tmp/gocache go test -race ./...

# 查看随机对照测试打印的输入/输出/判定日志
GOCACHE=/tmp/gocache go test -run TestRandomAgainstNaiveModel -v ./lessor/

go vet ./...
gofmt -l .
```

测试组成：

- `lessor_test.go`：规格示例、`g = max(ttl, MinTTL)` 与 Renew 恢复 `g`、
  `x == now` 到期、积压跨 Tick 与同 `x` 按 id 排序、键移动/升序/容量先判、
  检查点与切换推导、从态 TTL 口径、角色错误与拒绝不改状态、非法配置边界；
- `sim_test.go`：与按规则线性扫描实现的朴素模拟器对照 **2000 组**随机操作
  序列（每组 120 步），逐步比对返回值与完整状态快照，并在全新引擎上重放
  验证撤销序列与到期时刻逐字节一致；失败时及抽样通过时打印输入、输出与
  判定依据；
- `heap_test.go`：堆/映射一致性、`tickPeeks` 检视预算（100 与 10000 档）、
  以及 8 goroutine 并发压测（配合 `-race`）。
