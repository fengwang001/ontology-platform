# dimcache：CDC 驱动的维表查询缓存与失效

`dimcache` 实现一个由变更数据捕获（CDC）事件驱动的维表查询缓存。在上游变更事件
延迟、乱序、重复到达，且读取回填与失效相互交错的情况下，它保证：

> 在事件队列清空且无未完成读取令牌后，任意键的缓存结果与“无缓存、直接读源头”
> 的结果一致，且该最终状态可复现（同一输入脚本产生同一最终状态）。

## 核心概念

- **源头 `Source`**：版本化 KV。每次更新或删除都使该键版本加一；删除不物理清除，
  而是留下**墓碑**（`Deleted=true`），对墓碑再次删除版本继续递增。键从未存在时
  读取到的版本为 `0` 且 `found=false`。
- **CDC 事件 `Event`**：`{Key, Version, Kind(Upsert/Delete), Value}`，经
  `EventQueue` 投递。队列允许乱序、重复、延迟，FIFO 只决定投递次序。
- **栅栏 `fence[key]`**：缓存为每个键维护的“已收事件最大版本”。
- **缓存条目 `Entry`**：`{Version, Value, Negative}`，负缓存与正缓存共用同一结构。
- **读取令牌 `Token`**：两步读取的第一步产物，一次性使用。

## 栅栏规则（失效）

投递事件 `e` 时（`Cache.Deliver`）：

1. `e.Version <= fence[key]`：更旧（stale）或重复（duplicate）事件，**什么都不做**，
   不推进栅栏、不触碰缓存。
2. `e.Version > fence[key]`：把栅栏推进到 `e.Version`；若缓存条目版本
   `entry.Version < e.Version`，删除该条目（无论正/负缓存）。

因此栅栏是该键所有已观察变更的高水位线，旧事件永远无法把缓存“冲回旧值”。

## 读取与回填规则（两步读取）

读取必须分两步，中间允许事件到达：

1. **取令牌 `IssueReadToken(key)`**：直接读源头当前状态（源头读取计数 +1），生成
   一次性 `Token{Key, Version, Value, Deleted}` 并登记为“未完成令牌”。
   - 源头无此行（版本 0）或墓碑行，令牌标记为 `Deleted`，回填后成为**负缓存**。
2. **回填 `Backfill(token)`**，按顺序检查：
   1. 令牌不存在 / `nil` → `ErrTokenUnknown`；
   2. 令牌已使用 → `ErrTokenUsed`；
   3. 回填下限：`token.Version < fence[key]` → `ErrTokenStale`，拒绝写入
      （`token.Version == fence` 为边界，**接受**）；
   4. 全部通过才写入缓存（含负缓存），令牌标记为已用。

下限比较在写缓存前完成，所以“读取发生在旧版本、回填前新版本事件到达”的交错
只会得到一次干净的拒绝，不会污染缓存。过期令牌在拒绝时被回收（它永不可能再被
接受），保证系统可以收敛到静止；成功用过的令牌保留为 `used`，以便重复使用时
仍能与“未知令牌”区分。

`Query` 只查缓存（含负缓存命中），未命中不读源头；源头只在第一步读取时访问。

## 负缓存

“从无此键”（版本 0、found=false）与“已删除墓碑”都以 `Negative=true` 条目缓存，
携带其版本。删除事件到达时，旧的负缓存条目与正缓存条目一样按版本被驱逐；
墓碑复活（墓碑后再次 Update）产生更大版本的 Upsert 事件，同样正确覆盖。

## 边界条件

- 版本相等：事件 `Version == fence` 视为重复，忽略；回填 `token.Version == fence`
  视为满足下限，接受。
- 版本 `0`：表示源头从未见过该行；可以形成版本 0 的负缓存。
- 墓碑可重复删除，版本继续递增；只有“源头中从无此行”的删除才非法。
- 事件投递顺序任意：栅栏只进不退，最终高水位等于已投递事件的最大版本。
- 静止条件 `Quiescent()`：事件队列长度为 0 且未完成令牌数为 0。

## 错误类别（互不相同、可区分）

| 哨兵错误 | 触发条件 |
| --- | --- |
| `ErrEmptyKey` | 键为空（更新/删除/查询/签发/投递/入队） |
| `ErrTokenUnknown` | 回填令牌为 `nil` 或 ID 从未签发 |
| `ErrTokenUsed` | 令牌此前已成功用于回填 |
| `ErrDeleteMissing` | 删除源头中不存在（无墓碑）的键 |
| `ErrTrackedKeysExceeded` | 新键会使被跟踪的不同键数超过 `maxTrackedKeys` |
| `ErrTokenStale` | 回填令牌版本低于栅栏（回填下限） |

**任何一次拒绝都不改变源头、事件队列、缓存、栅栏或读源头计数**（失败不留痕）。
校验全部先于状态变更；跟踪键数上限由 `Cache` 在新键首次被观察时统一检查。

## 并发

`Source`、`EventQueue`、`Cache` 各自互斥（缓存用 `sync.RWMutex`），查询、签发、
回填、投递、源头更新可并发调用。注意签发时“读源头 + 登记令牌”在缓存锁内原子完成，
回填的三项判定与写入同样原子，因此交错执行不会出现半写状态。

## 日志

注入 `Logger`（实现 `Log(step string, fields map[string]any)`）后，每一步都会打印
输入事件/令牌、栅栏（旧值/新值/下限）、缓存条目与驱逐动作，以及判定依据
（`stale` / `duplicate` / `token_stale` / `token_used` / …）。传 `nil` 静默。

## 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 多次重复 + 逐步日志
go test -race -count=3 -v ./dimcache

# 只跑关键场景
go test -v -run 'TestBackfill|TestDeleteEvent|TestOutOfOrder|TestRejection|TestConvergence|TestConcurrent' ./dimcache

go vet ./...
gofmt -l .
```

测试覆盖：回填接受与拒绝、版本相等边界、删除/墓碑与负缓存、延迟/乱序/重复事件、
六类非法输入及拒绝前后全状态快照对比、并发交错（`-race`）、以及静止后逐键与
无缓存直读源头参照的一致性与可复现性。
