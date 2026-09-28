# incjoin — 两表内连接的增量差分维护

`incjoin.Joiner` 按批次对两张**多重集（multiset）表**做等值内连接，增量维护物化结果，
并输出每批"只反映变化"的差分。下游按顺序应用这些差分，始终得到与全量重算一致的连接结果。

## 数据模型

- 每张表是 `(Key, Value) -> multiplicity` 的多重集，重数为非负 `int64`；重数 0 表示行不存在。
- 一批输入 `Change{Left, Right}` 包含两侧各自的带符号变更行 `Row{Key, Value, Mult}`：
  - `Mult > 0`：插入（或增加重数）；
  - `Mult < 0`：删除（或减少重数）；
  - `Mult == 0`：非法，拒绝整批。
- 同一批内允许出现同一 `(Key, Value)` 的多行，按代数和聚合后再试应用；
  两侧可以同时非空（同一批同时改两张表）。

## 连接重数

等值内连接（按 `Key` 相等）的结果元组为 `(Key, LeftVal, RightVal)`，其重数等于两侧行重数之积：

```
JoinMult(key, lval, rval) = LeftMult(key, lval) * RightMult(key, rval)
```

例：左表 `(k,a)` 重数 2、`(k,b)` 重数 1，右表 `(k,x)` 重数 3，则连接结果：

```
(k,a,x) × 6     // 2 * 3
(k,b,x) × 3     // 1 * 3
```

键不在同一侧重合的行不出现在内连接结果中。

## 差分定义

记批前连接多重集为 `J_before`，批后为 `J_after`，一批的输出差分为

```
Diff(t) = J_after(t) - J_before(t)
```

- 只输出 `Diff(t) != 0` 的元组；
- 正数表示新增/增重，负数表示移除/减重；差分中不会出现 0；
- 按 `Key`、`LeftVal`、`RightVal` 字典序有序排列；
- 结果元组总数（所有重数之和）的变化等于各差分重数之和。

**关键性质**：从空结果开始，把每批返回的差分按顺序代数累加，任意时刻得到的多重集
都等于当时从两张基表全量重算的连接结果（测试 `TestDiffAgreesWithFullRecompute`、
`TestRandomizedDiffsMatchRecompute` 对此做持续校验）。

## 拒绝规则（整批原子，状态不变）

任何校验失败都会返回 `*RejectError`，其 `Reason` 给出可区分的原因；**被拒绝的批不会改动
两张基表，也不会改动物化结果**：

| Reason | 触发条件 |
|---|---|
| `EMPTY_KEY` | 任一侧变更行的连接键为空（不允许 null/空串） |
| `EMPTY_VALUE` | 任一侧变更行的值为空 |
| `INVALID_MULT_SIGN` | 变更重数为 0（必须为正或负） |
| `DELETE_NONEXISTENT_ROW` | 批后某 `(键,值)` 重数为负，即删除量超过现存重数（含删除从未存在的行） |
| `RESULT_TUPLE_LIMIT_EXCEEDED` | 批后连接结果总元组数（重数之和）超过 `MaxResultTuples` |
| `MULTIPLICITY_OVERFLOW` | 基表重数、连接乘积或结果总数超出 `int64` 范围（防御性拒绝） |

实现采用"先试算、后提交"：基表在副本上应用增量，只重算受影响连接键，全部校验通过后
才一次性替换真实状态；任何中途失败都不触碰正式状态。

## 并发与一致性

- `Apply` 可被多 goroutine 并发调用，内部以写锁串行化，每批是一个原子事务；
- `Snapshot` / `LeftSnapshot` / `RightSnapshot` 持读锁返回某个**完整已提交批**的视图，
  不会读到半批状态，视图中不存在负重数；
- 同一输入序列以相同顺序喂给新实例，逐批输出逐字节相同（输出排序确定，无 map 迭代泄漏）。

## API 速览

```go
j := incjoin.NewJoiner(incjoin.Options{
    MaxResultTuples: 10000,            // 0 表示不限制
    Logger:          myLogger,         // 实现 Logf；记录输入/输出差分/判定依据，可为 nil
})

diff, err := j.Apply(incjoin.Change{
    Left:  []incjoin.Row{{Key: "k", Value: "a", Mult: 2}},
    Right: []incjoin.Row{{Key: "k", Value: "x", Mult: -1}},
})
// err 为 *incjoin.RejectError 时整批被拒绝，状态不变。

snapshot := j.Snapshot() // []DiffEntry，按 key/leftVal/rightVal 有序
```

## 本地验证

```bash
# 全部测试（带竞态检测）
go test -race -v ./incjoin/

# 覆盖率
go test -coverprofile=coverage.out ./incjoin/
go tool cover -html=coverage.out

# 脚本化演示：正常差分 + 各类拒绝原因 + 输入/输出/判定依据日志
go run ./cmd/demo
```
