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

## 月度第 N 个星期几日程系列（`schedule` 包）

`schedule/` 实现按月重复的日程系列管理器，支持实例展开、单个实例的取消/改期，
以及“此次及以后”拆分系列。

### 数据模型

- 日期为公历 `"YYYY-MM-DD"`，合法范围 `1970-01-01`..`2200-12-31`；不存在的日期
  （如 `2024-02-30`）整体拒绝，错误码 `invalid_date`。
- 星期为 ISO 口径：周一=1 … 周日=7。
- 系列 = `(id, start, rule, 终止条件)`；`rule = {K, Nth, W}`：
  - `K`：间隔月数，`K >= 1`；
  - `Nth`：月内序号，取 `1..5` 或 `-1`（最后一个），`0` 及其他值拒绝；
  - `W`：星期，`1..7`。
- 终止条件 `Count(>=1)` 与 `Until(合法日期)` 恰好给一个，且 `Until` 不得早于 `Start`。

### 候选生成规则

1. `start` 所在月为第 0 个月，之后每隔 `K` 个月考察一个月；
2. 月内第 `Nth` 个星期 `W` 用 `firstOcc = 1 + (W - weekday(1号) + 7) % 7`、
   `day = firstOcc + 7*(Nth-1)` 计算；`Nth=-1` 时取该月最后一个星期 `W`；
3. 该月不存在第 5 个星期 `W`（`day > 当月天数`）时**跳过该月**——不顺延、不占名额；
4. 第 0 个月若候选早于 `start`（如 `start=1月31日` 而当月最后一个周五是 26 日），
   该候选**不算实例**，也不顺延到下一月；
5. `Count` 型：收满 `Count` 个实例停止；`Until` 型：生成到 `<= Until` 为止；
   候选晚于 `2200-12-31` 时停止；
6. `Count` 与 `Until` 一律按**原日期**判定，与改期后的实际日期无关。

### 名额归属与例外

- `Count` 统计所有实例，**被取消或被改期的实例照样占名额**。
- 取消：实例不再出现在展开结果中；若该实例已改期，同时释放其改期日。
  重复取消（含对已取消实例改期）以 `already_canceled` 拒绝。
- 改期：实例移动到任意新日期（可不满足规则）；再次改期移动到更新日期并释放旧改期日。
  新日期被同系列另一未取消实例（原位或改期而来）占用时以
  `reschedule_date_occupied` 拒绝。
- 被拒绝的操作不改变任何状态。

### “此次及以后”拆分

`Split(id, date, newID, newRule)` 中 `date` 必须是该系列的实例**原日期**
（含已取消或改期者；改期后的实际日不是合法拆分点）。“之前 / 及之后”一律按原日期比较：

- 原系列只保留严格早于 `date` 的实例：
  - 原为 `Count` 型：`Count = date 之前的实例数`（可以为 0，此时系列无实例但仍保留）；
  - 原为 `Until` 型：`Until = date - 1`（`date` 之前 0 实例时该 until 可早于 start，
    内部以序日存储，故允许早于 1970）；
  - 只保留这些实例对应的取消/改期例外，其余例外丢弃。
- 新系列：
  - `start = date`，按 `newRule` 展开；第一个候选是不早于 `date` 的首个符合新规则的
    日期（同月更早的候选丢弃，不顺延）；
  - 剩余名额：`Count` 型为 `原 Count - date 之前实例数`；`Until` 型沿用原 `Until`；
  - 无任何例外。

### 展开

`Expand(from, to)` 返回所有系列在半开区间 `[from, to)` 内的有效实例
`(SeriesID, Original, Actual)`：取消的不出现；改期实例按**实际日期**判定是否落窗；
结果按 `(实际日期, 系列 id, 原日期)` 升序。区间须非空且 `to - from <= 3660` 天，
否则以 `invalid_expand_range` 拒绝。

### 并发与确定性

- `Manager` 内部用单一 `sync.RWMutex` 串行化所有变更；展开在读锁下拷贝全部系列及其
  例外，因此看到的必然是某一时刻的完整系列快照，整体行为等价于某个串行顺序。
- 相同操作序列重放得到完全相同的展开结果（测试 `TestReplayDeterministic`）。

### 可区分错误码

| 错误码 | 触发条件 |
| --- | --- |
| `invalid_date` | 日期格式非法 / 日期不存在 / 超出 1970..2200 |
| `invalid_interval` | `K < 1` |
| `invalid_nth` | `Nth` 为 0、<-1 或 >5 |
| `invalid_weekday` | `W` 不在 1..7 |
| `invalid_end` | count 与 until 同时给或都不给 |
| `until_before_start` | until 早于 start |
| `duplicate_series_id` | 创建/拆分的新 id 已存在 |
| `unknown_series_id` | 操作引用了不存在的系列 |
| `not_instance_date` | 取消/改期/拆分日期不是该系列实例原日期 |
| `already_canceled` | 重复取消，或对已取消实例改期 |
| `reschedule_date_occupied` | 改期目标日被同系列另一实例占用 |
| `invalid_expand_range` | 展开区间为空、反转或跨度超过 3660 天 |

### 本地验证

```bash
# 全量测试（约 15 秒；含 40 个随机种子的朴素实现差分对照）
go test ./schedule/ -v

# 竞态检测（约 2.5 分钟）
go test -race ./schedule/

go vet ./...
gofmt -l .
```

测试日志会打印每个场景/随机操作的输入、输出与判定依据（`t.Logf`，配合 `-v` 查看）。
差分对照模型见 `schedule/naive_test.go`：它完全按规格**逐日扫描** 1970..2200 的日期，
直接检查“月偏移为 K 的倍数、星期为 W、是当月第 Nth 个 W（-1 为最后一个）”，
与正式实现（按月步进 + 算术定位候选）相互独立；`TestDifferentialAgainstNaive`
用 40 个随机种子重放包含创建、取消、改期、拆分（含非法参数）与展开的脚本，
逐一比对错误码、展开结果以及全时间轴滑窗展开。
