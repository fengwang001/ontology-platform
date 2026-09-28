# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

本仓库当前包含 `ontology` 包：一个**带前像校验的复制应用组件**，把携带前后像的变更事件按批应用到副本表，仅当前像与当前行完全一致时才落地变更，否则分类为冲突并跳过。

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

---

## 复制应用组件（`ontology` 包）

### 数据模型

- `Row map[string]string`：一行的列镜像，列名 → 列值。
- `Event`：一条变更事件，含 `Seq`（全局序号）、`Key`（行主键）、`Op` 与 `Before`/`After` 前后像。
- 三种操作及其前后像要求：

| 操作 | 前像 `Before` | 后像 `After` |
|---|---|---|
| `insert` | 必须为 `nil`（行不存在） | 必须非 `nil` |
| `update` | 必须非 `nil` | 必须非 `nil` |
| `delete` | 必须非 `nil` | 必须为 `nil` |

### 前像比较规则

行镜像按**列名与值的集合**比较（`rowsEqual`）：

1. 两边的列名集合必须完全相同（列数不同即不等）；
2. 每个共有列的值必须相等。

因此**“缺少某列”与“该列存在但值为空串”不相等**：

- 当前行 `{a:"1"}` 与前像 `{a:"1", b:""}` → 不一致（前像多一个空串列）；
- 当前行 `{a:"1", b:"2"}` 与前像 `{a:"1"}` → 不一致（前像缺列）；
- 只有列集合与每个值都相同时才判定前像匹配。

### 事件判定与冲突分类

事件按批内顺序逐条在**影子副本**上判定：

| 情形 | 分类 | 对副本的影响 |
|---|---|---|
| `insert` 目标键不存在且未超行数上限 | 应用（按后像插入） | 新增行 |
| `insert` 目标键已存在 | 冲突 `row_exists` | 跳过，现有行不被覆盖 |
| `update`/`delete` 目标行不存在 | 冲突 `row_missing` | 跳过 |
| `update`/`delete` 行存在但前像与当前行不完全一致 | 冲突 `before_mismatch` | 跳过 |
| `update` 前像匹配 | 应用（按后像整行覆盖） | 覆盖该行 |
| `delete` 前像匹配 | 应用（删除该行） | 删除行 |

规则要点：

- **冲突不是错误**：`Apply` 返回的 `error` 为 `nil`，冲突出现在 `BatchResult.Conflicts` 与累计冲突日志 `Store.Conflicts()` 中，可与错误明确区分。
- **冲突不改变副本**：被跳过的事件不写任何行。
- **冲突不阻断同批后续事件**：一条冲突只跳过自身，后续事件继续判定。
- **冲突事件仍占用序号**：冲突是合法事件，批成功后已处理序号按批内事件总数推进，下一批必须从其后连续提交。
- 删除会释放行数名额：同批内“先删后插”时，插入按删除后的行数判定上限。

### 整批拒绝（错误，可区分原因）

以下情况拒绝**整批**，返回 `*RejectError`，其 `Reason` 字段可区分：

| `Reason` | 触发条件 |
|---|---|
| `invalid_event` | 未知操作类型，或前后像组合不符合该操作的要求（含 `Index` 指向批内下标） |
| `seq_gap` | 事件序号与已处理序号不连续（批首必须为 `lastSeq+1`，批内逐条 +1；回退、跳号均拒绝） |
| `too_many_rows` | 某条插入实际落地时会使副本行数超过 `NewStore(maxRows)` 上限 |

**原子性**：拒绝发生在提交前，采用“先整批校验 → 影子副本逐条判定 → 全部通过后一次性提交”。被拒绝的批：

- 不改变副本任何行；
- 不增加冲突日志（批内已判定出的冲突一并回滚）；
- 不推进已处理序号（`LastSeq` 不变），因此调用方修正后须从原期望序号重新提交。

### 并发与一致性

- `Apply` 内部持写锁串行化；`Snapshot`/`LastSeq`/`Conflicts` 持读锁并在锁内深拷贝。
- 并发读取看到的永远是**某一批应用前或应用后的完整边界状态**，不会看到半批。
- `Snapshot()` 返回边界时刻的独立深拷贝，取出后无需持锁即可继续读，调用方修改返回的 `Row` 不影响副本。

### 确定性

同一输入序列（相同的批与事件、相同的 `maxRows`）在全新 `Store` 上反复计算，得到完全相同的副本状态、冲突集合与 `LastSeq`。组件不依赖 map 迭代顺序、时间或随机数。

### 判定日志

通过 `Store.SetLogger(NewTextJudgeLogger(w))` 注入日志器后，每条事件都打印一行，包含**输入**（序号、主键、操作、前后像）、**判定结果**（`applied` / `conflict:<kind>` / `rejected:<reason>`）与**依据**（例如“前像 {…} 与当前行 {…} 不完全一致”）。日志是观测输出，不属于被事务保护的副本状态。

注意：日志回调内不要回调同一个 `Store` 的方法（`Apply` 持锁期间调用会自锁）；`TextJudgeLogger` 只写入给定的 `io.Writer`，可安全使用。

### 用法示例

```go
store := ontology.NewStore(10000) // 副本行数上限

res, err := store.Apply([]ontology.Event{
    {Seq: 1, Key: "row-1", Op: ontology.OpInsert, After: ontology.Row{"a": "1", "b": "2"}},
    {Seq: 2, Key: "row-1", Op: ontology.OpUpdate,
        Before: ontology.Row{"a": "1", "b": "2"}, // 必须与当前行逐列完全一致
        After:  ontology.Row{"a": "1", "b": "3"}},
})
if err != nil {
    var rej *ontology.RejectError
    if errors.As(err, &rej) {
        // rej.Reason: invalid_event / seq_gap / too_many_rows
    }
}
// res.Applied 实际应用数；res.Conflicts 为本批冲突（不是错误）

snap := store.Snapshot()
if row, ok := snap.Get("row-1"); ok { /* ... */ }
```

### 本地验证方法

```bash
# 1) 格式化与静态检查
gofmt -l . && go vet ./...

# 2) 全量测试（含前像整行不等冲突、缺列与空串区分、各类非法输入、
#    序号不连续、行数超限、原子回滚、重放确定性、并发读边界）
go test -race -v ./ontology/

# 3) 覆盖率
go test -coverprofile=coverage.out ./ontology/
go tool cover -func=coverage.out
```

关键测试入口：

- `TestRowsEqual` / `TestConflictMissingColumnVsEmptyString`：集合比较与缺列/空串区分；
- `TestConflictBeforeMismatchWholeRow` / `TestConflictRowMissing` / `TestConflictInsertRowExists`：三类冲突；
- `TestRejectInvalidEvents` / `TestRejectSeqGap` / `TestRejectTooManyRows`：三类可区分的拒绝原因及原子回滚；
- `TestMixedBatchConflictDoesNotBlockLaterEvents` / `TestDeterministicReplay`：冲突不阻断后续事件、重放确定性；
- `TestConcurrentReadsSeeBatchBoundaries`：`-race` 下并发读取只看到批边界；
- `TestJudgeLoggerOutput`：日志中包含输入、判定结果与依据。
