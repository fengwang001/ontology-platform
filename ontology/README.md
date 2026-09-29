# 窗口状态管理器（Window State Manager）

带清理（cleanup）与复活（revive）语义的并发安全窗口状态管理器，位于包 `ontology`
（见 `window_manager.go`）。每个窗口按到达事件累计数值，累计值从下往上越过阈值时
产出触发事件；窗口支持清理冻结、由迟到事件复活、以及对已清理窗口的回收。

## 构造与 API

```go
m, err := ontology.NewManager(threshold float64, maxWindows int) (*Manager, error)

(EventResult, error)  m.Event(id string, value float64)      // 普通事件（隐式建窗）
(EventResult, error)  m.LateEvent(id string, value float64)  // 迟到事件（可复活）
error                 m.Cleanup(id string)                   // 清理（冻结）
(bool, error)         m.Reclaim(id string)                   // 回收（true=已删除）
(WindowSnapshot, bool) m.Snapshot(id string)                 // 单窗口快照
[]WindowSnapshot       m.SnapshotAll()                        // 全部窗口快照
int                    m.TotalFireCount()                     // 现存窗口触发数之和
int                    m.LifetimeFireCount()                  // 生命周期累计触发数（单调）
```

`EventResult.Triggers` 携带本次产出的触发事件，`Seq` 为该窗口内从 1 开始的触发序号。

## 窗口状态迁移

窗口有三种状态：`active`、`cleaned`、`revived`。

```text
                 首次 Event(id,v)                    Cleanup(id)
   (不存在) ───────────────────────▶ active ─────────────────────▶ cleaned
                                        ▲                             │
                                        │ Cleanup 对非 active 是 no-op │
                                        │                             │ LateEvent(id,v)
                                        │ （revived 不回退）            │ （复活只取这一条）
                                        │                             ▼
                                        └──────────────────────── revived
                                                                      │
                                                                      │ Event / LateEvent
                                                                      ▼
                                                                   revived（累加，不触发）
```

各状态下的事件语义：

| 状态 | `Event`（普通事件） | `LateEvent`（迟到事件） | `Cleanup` | `Reclaim` |
| --- | --- | --- | --- | --- |
| `active` | 累加并按越过次数触发 | 同普通事件（可触发） | → `cleaned`，值冻结 | 不删除（返回 false） |
| `cleaned` | 丢弃（冻结，不累加不触发） | **复活**：→ `revived` | no-op | 删除窗口（返回 true） |
| `revived` | 累加，**永不触发** | 累加，**永不触发** | no-op（保持 revived） | 保留（返回 false） |

### 触发规则

- 只有 `active` 窗口会触发。窗口首次事件到达时隐式创建为 `active`。
- 触发次数按“从下往上越过阈值”的边界数计算，一条事件可触发多次：
  `crossings = floor(newTotal/threshold) - floor(oldTotal/threshold)`。
  例如阈值 10，11 后再到 36（+25）越过 20、30，产出 2 条触发事件。
- 恰好落在阈值边界上算一次越过（`floor` 语义）。

### 清理与复活

- **清理冻结**：`Cleanup` 只把 `active` 改为 `cleaned`；累计值与已触发次数原样保留，
  此后普通事件既不累加也不触发。
- **复活只服务一条事件**：对 `cleaned` 窗口调用 `LateEvent` 时，丢弃全部冻结历史
  （累计值与窗口历史触发次数清零），累计值直接取这一条迟到事件的值，
  状态变为 `revived`，且这条事件本身不触发。
- **复活后抑制触发**：`revived` 窗口对之后的普通事件与迟到事件都正常累加，但永久不再触发。
- **回收**：`Reclaim` 只删除 `cleaned` 窗口；`revived` 与 `active` 窗口保留。
  已回收窗口从管理器消失，之后对它的迟到事件按“从未存在”处理。

## 复活与回收的互斥

复活（`LateEvent` 在 `cleaned` 上的迁移）与回收（`Reclaim` 删除 `cleaned`）是对同一
窗口的一对竞争操作。实现上，两者都在 **Manager 全局写锁的同一个临界区** 内完成
“查找 → 状态判定 → 生效（改状态或删除 map 项）”，因此：

- 一个 `cleaned` 且未复活的窗口，最终结局 **要么被回收、要么被复活**，二者不可能同时发生；
- 若回收先进入临界区：窗口被删除，迟到事件随后得到 `ErrWindowNotFound`（回收获胜）；
- 若复活先进入临界区：窗口变为 `revived` 且保留，回收随后返回 `false`（复活获胜）。

并发测试 `TestConcurrentReclaimVsRevive` 对 200 个窗口同时发起回收与迟到事件，
断言两个结局集合互不相交且并集恰好覆盖全部窗口，并逐窗口核对最终状态。

### 并发模型

- `Manager.mu`（`sync.RWMutex`）保护窗口 map（创建、删除、查找）。
- 每个窗口一把 `sync.Mutex`，保护该窗口的 `total / fireCount / state`；
  同一窗口的事件被串行化，不同窗口可并行累加。
- 加锁顺序固定为 `Manager.mu → window.mu`，代码中不存在反向获取，避免死锁。
- `Snapshot / SnapshotAll / TotalFireCount / LifetimeFireCount` 均可与写操作并发执行；
  `LifetimeFireCount` 由原子计数器支撑，生命周期内单调不减。
  （复活清零与回收删除会让“现存窗口触发数之和”下降，但不影响生命周期累计值。）

## 四类可判定错误

错误均为哨兵错误，使用 `errors.Is` 判定，彼此互不相同；任何拒绝路径都在修改状态之前返回：

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidID` | 窗口标识非法：空串或纯空白（空格/制表符等） |
| `ErrInvalidValue` | 事件值非法：负数或 NaN（普通事件与迟到事件一致） |
| `ErrWindowNotFound` | 对从未创建过、或已被回收的窗口发迟到事件；清理/回收不存在的窗口 |
| `ErrTooManyWindows` | 隐式创建新窗口时窗口数已达 `maxWindows` 上限 |

被拒调用不改变任何状态（测试用拒绝前后快照逐一比对），拒绝后管理器可继续正常使用。
构造器对 `threshold <= 0 / NaN` 与 `maxWindows <= 0` 也返回错误。

## 本地验证

需 Go 1.26+（仓库已声明 `go 1.26.5`）。

```bash
# 全量测试
go test ./...

# 带竞态检测、重复运行与详细日志（日志含每步输入、结果与判定依据）
go test -race -count=3 -v ./ontology/

# 只跑并发互斥用例
go test -race -run TestConcurrentReclaimVsRevive -v ./ontology/

# 静态检查与格式
go vet ./...
gofmt -l .
```

## 测试覆盖

`window_manager_test.go` 覆盖：

- `TestThresholdTriggers`：隐式建窗、单次越过、单条事件连续越过多次、`Seq` 连续；
- `TestCleanupFreeze`：清理后冻结保留，普通事件被丢弃；
- `TestReviveSingleEvent`：复活只取迟到事件的值、丢弃冻结历史、不触发；
- `TestRevivedSuppressesTriggers`：复活后普通/迟到事件均累加但不触发；
- `TestReclaimKeepsRevived`：回收删除已清理窗口、保留已复活窗口、活跃窗口不可回收；
- `TestRejectedInputsKeepState`：四类非法输入分别返回不同哨兵错误、拒绝前后快照不变、拒绝后仍可用；
- `TestConcurrentAccumulation`：并发事件下同一窗口累计值与触发次数精确，并发读无撕裂；
- `TestConcurrentReclaimVsRevive`：回收与迟到并发的互斥与完备划分；
- `TestConcurrentMixedOperations`：事件/清理/迟到/回收/快照/计数混合并发后状态自洽。
