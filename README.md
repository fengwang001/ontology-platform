# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## 分组增量去重计数（`dedup` 包）

`dedup.Counter` 在撤回式变更流上按分组增量维护去重计数：变更以**批**为单位
提交，组件跟踪每个组内每个值的多重性，并输出各组去重计数的净变化，使下游
**按顺序应用日志**即可始终重放出正确的去重计数。

### 多重性（Multiplicity）

- 每条变更为 `Entry{Group, Value, Delta}`，`Delta` 只允许 `+1`（插入）或
  `-1`（撤回）。
- 对每个 `(组, 值)` 维护插入与撤回折叠后的**净次数**（多重性，非负）。
- 组的**去重计数** = 该组内多重性为正的值的个数。同一值插入 3 次，多重性
  为 3，但只贡献 1；撤回至多重性 0 后不再贡献，且该值（及空组）从视图移除。

### 批内校验与折叠（Fold）

- 一批条目**按顺序**逐条校验，每条都作用在"已提交状态 + 批内此前各条已生效
  结果"之上：撤回时当前多重性为零即拒绝整批。
  - `[插入 v, 撤回 v]` 合法（折叠为 0）；颠倒为 `[撤回 v, 插入 v]` 则第一条
    就被拒绝——两者折叠结果相同，但顺序不同判定不同。
- 同一值在批内的多次插入/撤回先折叠为净增量，再按"每个值是否跨越零点"
  求和得到组去重计数的净变化，避免逐条更新的中间态误差。
- **原子性**：任一条目非法则整批拒绝，多重性、`Snapshot()` 视图与已产生的
  `Log()` 都不改变（批内前段条目的效果随整批丢弃）。

### 输出规则

- 成功的 `Apply` 返回 `[]GroupChange` 并向日志追加同一条记录：
  `GroupChange{Group, Before, After}`，其中 `After-Before`（`Delta()`）即该组
  本批的净变化（可能为 0；净变化为 0 的组仍会出现，且反映 Before/After）。
- 输出按**组名字典序**排列，顺序确定。
- 下游重放：从全 0 视图起，按日志顺序对各组累加 `Delta()`；某组计数折叠到 0
  时该组即不存在。重放结果与 `Snapshot()` 逐字段一致。

### 拒绝原因（可区分）

| `RejectReason`         | 触发条件                                   | `BatchError.Index` |
| ---------------------- | ---------------------------------------- | ------------------ |
| `ReasonEmptyGroup`     | 组名为空字符串                            | 条目下标           |
| `ReasonEmptyValue`     | 值为空字符串                              | 条目下标           |
| `ReasonInvalidSign`    | `Delta` 非 `+1/-1`（如 0、+2、-2）        | 条目下标           |
| `ReasonWithdrawZero`   | 撤回时当前净次数为零（含批内此前各条生效后）| 条目下标           |
| `ReasonTooManyEntries` | 批条目数超过 `NewCounter` 设定的上限       | `-1`（批级错误）   |

错误为 `*BatchError`，可按 `Reason` 机器判别；`Reason.Explain()` 给出判定依据
的文字说明。

### 并发与确定性

- `Apply` / `Snapshot` / `Log` 均可并发调用；读操作在读写锁保护下返回深拷贝，
  单次读取得到的视图逐字段一致，调用方可自由修改返回值。
- 同一输入序列反复计算得到完全相同的输出（字典序 + 值类型日志，无 map 迭代
  随机性泄漏）。

### 本地验证

```bash
# 竞态检测 + 详细日志（日志含每个用例的输入条目、输出条目与接受/拒绝判定依据）
go test -race -v ./dedup

# 仅跑关键用例
go test -run 'TestMultiplicitySameValue|TestBatchFoldAddThenRemove|TestBatchOrdering|TestInvalidInputs|TestLogReplay|TestConcurrentApplyAndRead|TestDeterministic' -v ./dedup

# 覆盖率与静态检查
go test -coverprofile=/tmp/dedup.cover ./dedup && go tool cover -func=/tmp/dedup.cover
gofmt -l ./dedup && go vet ./dedup
```

