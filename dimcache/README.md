# dimcache：CDC 驱动的维表查询缓存失效

`dimcache` 在源头系统之上提供维表查询缓存。上游变更以 CDC 事件形式
延迟、可能乱序到达；缓存在“读取回填”与“失效投递”交错的情况下仍
保证最终与无缓存直接读源头一致，且执行过程可复现（每一步都打印
输入、栅栏、缓存与判定依据）。

## 数据模型

- 每个键在源头带单调递增的版本号 `version >= 1`。
- `Update`（含复活已删除行）与 `Delete` 都使版本加一。
- 删除在源头留下**墓碑（tombstone）**，版本继续递增；墓碑同样参与
  事件投递与缓存判定。
- 事件 `Event{Key, Version, Deleted, Value}` 先进先出排队，由
  `DeliverNext` 投递；事件只负责推进栅栏/失效，不负责填缓存。

## 栅栏（Fence）规则

每个键维护“已收事件的最大版本”作为栅栏：

1. 收到事件版本 `ev.Version <= fence`：**更旧或重复，不做任何事**。
   版本相等的重复事件同样被忽略（相等边界，保留现状）。
2. `ev.Version > fence`：推进栅栏到 `ev.Version`；若该键存在缓存条目
   且 `entry.version < ev.Version`，删除该条目（正缓存与负缓存一视同仁）。
   `entry.version == ev.Version` 的条目保留。

事件本身从不写缓存值，因此延迟事件只做失效、不做回填，避免旧值复活。

## 两阶段读取与回填

1. `BeginRead(key)`：读一次源头并签发**一次性令牌**（token），令牌捕获
   当时的 `(version, deleted, value)`。空键的源头版本为 `0`。
2. `Backfill(tokenID)`：用令牌写缓存。接受条件：
   - 令牌必须存在且未被成功消费（未知 / 已用分别报错）；
   - **令牌版本不低于回填下限**，即 `token.version >= fence(key)`，
     否则以 `ErrTokenStale` 拒绝；版本恰好相等（`== fence`）是边界，
     予以接受；
   - 键已有缓存条目时还要求 `token.version >= entry.version`，防止倒退；
   - 新键写入受跟踪键数上限约束，超限拒绝。
3. 被拒时**失败不留痕**：不消费令牌、不改缓存、不推栅栏、不增读计数，
   调用方可用同一令牌重试，也可重新 `BeginRead`。只有接受才把令牌
   移入“已用”集合。
4. `Query(key)` 是读穿路径：命中（含负缓存）直接返回；未命中则内部
   完成上述两步。若回填恰好被新来的事件判定为 stale，则丢弃该快照、
   重新读取并重试；容量满时退化为直接返回源头快照（等同直读）。

## 负缓存（Negative Caching）

墓碑/不存在的键与正常值使用同一条目结构（`Deleted=true`），同样写入
缓存、同样受栅栏保护：

- 命中墓碑条目时 `Query` 返回 `found=false`，不回源；
- 新版本事件（删除或复活）到达时，版本过旧的墓碑条目被清除；
- 对从未存在的键，`BeginRead` 得到版本 `0` 的墓碑快照，在栅栏仍为
  `0` 时可以回填为负缓存（`0 >= 0` 边界）。

## 边界汇总

| 情形 | 判定 |
| --- | --- |
| 事件版本 `< fence` | 忽略，不做任何事 |
| 事件版本 `== fence` | 重复，忽略；同版本缓存条目保留 |
| 事件版本 `> fence` | 推进栅栏；清掉 `entry.version < ev.Version` 的条目 |
| 令牌版本 `< fence` | 回填拒绝（`ErrTokenStale`），不留痕 |
| 令牌版本 `== fence` | 回填接受（相等边界） |
| 删除不存在/已墓碑的行 | 整体拒绝（`ErrDeleteMissing`） |
| 跟踪键数已满再写新键 | 回填拒绝（`ErrTooManyTrackedKeys`），不留痕 |

## 错误类别（互不相同、可区分）

| 哨兵错误 | 触发条件 |
| --- | --- |
| `ErrEmptyKey` | 任意操作使用空键 |
| `ErrInvalidVersion` | 事件版本号非正 |
| `ErrInvalidTombstone` | 墓碑事件携带值 |
| `ErrDeleteMissing` | 删除不存在或已删除的行 |
| `ErrUnknownToken` | 回填引用从未签发的令牌 |
| `ErrTokenUsed` | 回填引用已成功消费的令牌 |
| `ErrTokenStale` | 令牌版本低于栅栏/现有缓存版本 |
| `ErrTooManyTrackedKeys` | 跟踪键数超上限 |

任何拒绝都不会改变源头、事件队列、缓存、栅栏或读源头计数。

## 并发与收敛

全部方法在同一互斥锁下串行化临界区，`Update`/`Delete`/`Emit`/
`DeliverNext`/`BeginRead`/`Backfill`/`Query` 可被多个执行体并发调用。

收敛条件：**事件队列清空且无未完成令牌**（`Quiescent() == true`，前提
是每次源头变更都最终发出了对应事件）。此时每个存活缓存条目都满足
`entry.version >= fence == 源头版本`，故：

- `Query(key)` 与无缓存参照 `DirectQuery(key)` 的 `(value, found)` 完全一致；
- 不存在版本落后于源头的缓存条目。

注意：收敛之前允许查询读到稍旧的值（最终一致），但绝不允许被已失效
事件“复活”的更旧值。

## 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 详细日志（每个用例通过 t.Logf 打印每步输入/栅栏/缓存/判定）
go test -race -v ./dimcache

# 并发收敛用例重复运行，排除时序抖动
go test -race -count=20 ./dimcache

# 覆盖率
go test -cover ./dimcache

# 静态检查与格式
go vet ./...
gofmt -l .

# 可复现的端到端小演示
go run ./cmd/server
```

若环境中的 `GOCACHE` 位于只读文件系统，可先 `export GOCACHE=/tmp/gocache`。
