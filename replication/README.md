# replication — 带前像校验的复制应用组件

把携带**前像（before-image）与后像（after-image）**的变更事件逐条应用到内存副本表。
只有当前像与当前行**完全一致**时才应用变更；否则该事件被分类为**冲突**并跳过。
整批要么完整生效，要么完全不生效；并发读者只能看到某一批的完整边界状态。

## 数据模型

| 类型 | 含义 |
| --- | --- |
| `Row` | 一行的列镜像，`map[string]string`，即「列名 → 值」的集合 |
| `Event` | 变更事件：`Seq`、`Op`、`Key`、`Before`、`After` |
| `Conflict` | 冲突记录：序号、主键、操作、冲突类别、前像、当前行、依据 |
| `BatchResult` | 一批的结果：`Applied`、`Conflicts`、`LastSeq`、`RowCount` |
| `RejectError` | 整批拒绝错误，`Reason` 可取三种可区分原因 |

## 前像比较规则

前像与当前行按**列名与值的集合**整体比较（`rowsEqual`）：

1. 两侧列集合必须完全相同，每个列的值也必须相同；
2. **缺列 ≠ 空串**：当前行有列 `note=""` 而前像不含 `note` 键，视为不一致；
   反之亦然。日志中缺列渲染为 `<missing>`，空串渲染为 `""`，二者一眼可分；
3. 行是否「存在」由对副本表的主键查找决定，与镜像是否为空 map 无关。

## 每种操作的前后像要求与冲突分类

| 操作 | 前像 `Before` | 后像 `After` | 冲突类别（冲突不是错误，仅跳过） |
| --- | --- | --- | --- |
| `INSERT` | 必须为 `nil`（行不存在） | 必须非 `nil` 且至少一列 | `ROW_EXISTS`：插入时行已存在 |
| `UPDATE` | 必须非 `nil` 且至少一列 | 必须非 `nil` 且至少一列 | `ROW_MISSING`：行不存在；`BEFORE_MISMATCH`：前像与当前行不完全一致 |
| `DELETE` | 必须非 `nil` 且至少一列 | 必须为 `nil` | `ROW_MISSING`：行不存在；`BEFORE_MISMATCH`：前像与当前行不完全一致 |

- 冲突事件被跳过，**不改变副本**；同批其余事件照常应用；冲突追加到冲突日志。
- 冲突是 `BatchResult.Conflicts` 中的**正常结果**，`Apply` 对冲突返回 `nil` error。
- `Conflict.Before` / `Conflict.Current` 保留判定时两侧镜像，便于审计与重放。

## 整批拒绝规则（错误，可区分原因）

`Apply` 先在持锁的临时副本上干跑完整批，任何一步失败都返回 `*RejectError`
且**副本、冲突日志、已处理序号均不变**：

| `RejectError.Reason` | 触发条件 |
| --- | --- |
| `ILLEGAL_EVENT` | 操作未知、主键为空、序号非正、列名为空，或前后像不符合上表要求 |
| `SEQUENCE_GAP` | 事件序号未从 `LastSeq()+1` 开始逐条严格 `+1`（批中段内跳号同样拒绝） |
| `REPLICA_FULL` | 接受某条 `INSERT` 会使副本行数超过 `NewStore(maxRows, …)` 的上限（删除腾位后可插入；恰好等于上限允许） |

可用 `errors.Is(err, replication.ErrIllegalEvent)` / `ErrSequenceGap` / `ErrReplicaFull`
区分原因；`RejectError` 还带 `EventIndex`、`Seq`、`ExpectedSeq`、`Detail`。

**校验顺序**：单事件合法性 → 序号连续性 → 前像校验下模拟执行（含行数上限）
→ 全部通过才一次性提交。因此序号为乱序的非法事件会先报 `ILLEGAL_EVENT`。

## 序号与提交语义

- 序号必须全局连续：第一批从 `1` 开始，之后每批承接 `LastSeq()`。
- 冲突事件也消费序号（事件确实被处理过，只是未应用），因此批内序号按事件条数推进。
- 提交是单次状态替换：`s.rows = working`、`lastSeq += len(events)`、追加冲突、
  输出判定日志，全部在写锁内完成。

## 并发与确定性

- `sync.RWMutex`：读（`Get`/`Snapshot`/`RowCount`/`LastSeq`/`Conflicts`）走 `RLock`
  并发进行，`Apply` 持写锁整批提交。读者不会看到半批。
- 所有对外返回的镜像/快照/冲突均为深拷贝，调用方无法篡改内部状态。
- 输出确定：差异列按列名字典序定位，行镜像以键序固定的 JSON 渲染，
  同一输入序列反复计算得到逐字节相同的日志与相同结果。

## 判定日志

`NewTextLogger(w)` 输出稳定单行文本，`NewNopLogger()` 丢弃；也可实现
`DecisionLogger` 接口接入其他日志系统。每条判定包含**输入**（seq/op/key/前后像）、
**结果**（`APPLIED` / `CONFLICT` + 冲突类别）与**依据**；整批拒绝输出
`[rejection] reason=…` 行。示例：

```text
[decision] seq=4 op=UPDATE key="user-1" before={"name":"alice2"} after={"name":"alice3"} -> CONFLICT BEFORE_MISMATCH: before-image does not equal the current row: column "note" missing (<missing>) in before-image but present in current row with value "" (a missing column is not the empty string)
[rejection] reason=SEQUENCE_GAP: event seq 9 is not consecutive; expected 6
```

## 快速上手

```go
store := replication.NewStore(100, replication.NewTextLogger(os.Stdout)) // <=0 表示不限行数
res, err := store.Apply([]replication.Event{
    {Seq: 1, Op: replication.OpInsert, Key: "k", Before: nil, After: replication.Row{"a": "1"}},
})
// err 为 *replication.RejectError 时整批被拒绝；res.Conflicts 为冲突（非错误）
```

## 本地验证

```bash
# 全量测试（含竞态检测）
go test -race ./...

# 详细输出 + 覆盖率
go test -race -v -cover ./replication/

# 可运行文档（断言示例输出逐字节一致）
go test -run Example -v ./replication/

# 静态检查与格式
go vet ./...
gofmt -l .
```

## 文件结构

| 文件 | 职责 |
| --- | --- |
| `types.go` | `Op`/`Row`/`Event`/`Conflict`/`BatchResult` 等核心类型 |
| `errors.go` | `RejectError`、三种拒绝原因与哨兵 |
| `image.go` | 镜像判等、差异定位（缺列/空串区分）、事件合法性校验 |
| `store.go` | `Store`：整批干跑、前像校验、原子提交与并发读写 |
| `logger.go` | `DecisionLogger` 接口、文本日志器 |
| `*_test.go` | 冲突、镜像语义、非法输入、拒绝无痕、确定性、并发边界等测试 |
