# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/demo

# 编译后运行
go build -o bin/demo ./cmd/demo
./bin/demo
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./join
go test -run TestDeltaMatchesFullRecompute ./join

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 两表内连接增量差分维护（`ontology/join`）

`join.Joiner` 对两张**多重集表**（multiset：同一行可带非负重数）按等值键做内连接，
并以**批次**为单位输出**只反映本批变化**的差分。下游按顺序把差分累加到上一批的
物化结果上，始终得到当前批的完整连接结果。

### 连接重数

对连接键 `k`，结果元组 `(k, lv, rv)` 的重数等于两侧行重数的**乘积**：

```
mult_result(k, lv, rv) = mult_left(k, lv) * mult_right(k, rv)
```

左行 `(k, lv)` 或右行 `(k, rv)` 不存在时重数视为 0，乘积即为 0（不出现在结果中）。

### 差分定义

设批前全量连接结果为 `J_old`，批后为 `J_new`，则本批输出：

```
Δ(t) = J_new(t) - J_old(t)
```

输出只包含 `Δ(t) != 0` 的元组，`Δ>0` 为新增重数、`Δ<0` 为撤销重数，并按
`(Key, LeftValue, RightValue)` 字典序有序排列。同批内同增同减恰好抵消
（`Δ=0`）的元组不输出。

增量推演只扫描受影响连接键，对每个键把差分拆成两部分（`m_old`/`m_new` 为批前/后行重数）：

```
(左行 lv 变更)  Δ = (m_l_new - m_l_old) * m_r_old        // 变更左行 × 批前全部右行
(右行 rv 变更)  Δ = m_l_new       * (m_r_new - m_r_old)  // 变更右行 × 批后全部左行
```

第二项使用批后左行集合，因此同一批左右两侧同时插入时，`(新左行, 新右行)` 也不会遗漏。

### 原子性与拒绝原因

校验全部通过后才提交；任一项失败则**整批拒绝**，两张表与物化结果保持批前状态，
不会出现半批或负重数。原因以结构化的 `*join.RejectError` 返回，可 `errors.Is` 判定：

| Reason（`errors.Is` 哨兵） | 含义 |
| --- | --- |
| `null_value` (`ErrNullValue`) | 连接键或负载值为空（NULL） |
| `invalid_delta` (`ErrInvalidDelta`) | 行重数变更量为 0（变更符号非法） |
| `delete_nonexistent` (`ErrDeleteNonexistent`) | 删除量超过现存重数，批后该行负重数 |
| `result_too_large` (`ErrResultTooLarge`) | 批后不同结果元组数超过 `Options.MaxResultTuples` |
| `multiplicity_overflow` (`ErrMultiplicityOverflow`) | 重数乘积或累加超出 int64 |

错误还携带 `Side`（left/right）、`Key`、`Value` 与 `Detail`，便于定位触发行。

### 并发语义

- `Apply` 串行化、`Snapshot` 持读锁返回**深拷贝**；二者可被并发调用。
- 并发读到的视图要么是批前、要么是批后的完整状态，绝不会读到半批或负重数。
- 物化结果始终与 `join.FullJoin(left, right)` 全量重算一致。
- 对同一输入序列，任意次重放产生逐字节相同的输出（map 迭代不确定性不影响结果，
  输出统一排序）。

### 日志

通过 `Options.Logger` 注入 `*slog.Logger`（缺省写 stderr）。每批打印三条事件：
`join.apply.start`（输入）、`join.apply.accepted`（判定依据 + 输出差分 + 结果元组数）、
`join.apply.rejected`（判定依据 + 可区分原因，差分恒为 `[]`）。运行 `go run ./cmd/demo`
可查看完整示例。

### 本地验证方法

```bash
go run ./cmd/demo                 # 脚本化演示，每批后自动与全量重算交叉校验
go test -race ./join/             # 含 3000 轮随机差分 vs 全量求差、并发读写一致性
go test -run TestConcurrent ./join -race -count=5
```

关键测试：

- `TestMultiplicityIsProduct`：连接重数等于两侧重数之积。
- `TestSimultaneousChangeBothSides` / `TestSimultaneousInsertsOnBothSides`：同批改两张表。
- `TestDeltaMatchesFullRecompute`：随机批次序列下，返回差分恒等于批后/批前全量之差，
  且仅按顺序累加差分的外部视图始终等于全量重算。
- `TestReject*`：空值、零增量、删除致负重数、结果超限、溢出，且拒绝后状态逐字节不变。
- `TestConcurrentApplyAndSnapshotV2`：8 写者 + 4 读者并发，任意快照与全量重算一致。
- `TestDeterministicReplay`：同一输入序列反复计算输出完全相同。
