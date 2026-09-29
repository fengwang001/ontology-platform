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

## 反连接增量物化视图（`ontology` 包）

`ontology/antijoin.go` 实现反连接（anti-join）增量物化视图：

- 左侧行在结果中，当且仅当不存在键与其相等的右侧行。
- 左侧、右侧分别以 `(Side, ID)` 为行标识；一次提交是一批 `Change`。

### 空值语义

键为 `nil`（`Change.Key == nil`）表示空值，采用 SQL 风格的相等规则：

- 空值与任何值都不相等，**包括与另一个空值也不相等**（`NULL = NULL` 为假）。
- 空值键的左侧行**永远**在结果中：没有任何右侧行能匹配它。
- 空值键的右侧行不匹配任何左侧行；它不参与任何键的计数。
- 只有两个非空键且字符串值相等时才算匹配。

### 成员资格判定

视图维护：

- 两侧行集合 `left` / `right`（标识 → 键）；
- 每个**非空**键的右侧行数 `rightCount`（仅保留计数为正的键）；
- 当前结果中的左侧行集合 `members`。

左侧行的成员资格为：

- 键为空值 → 在结果中；
- 键为非空值 `k` → 在结果中当且仅当 `rightCount[k] == 0`。

### 变更日志规则

`Commit` 在成功应用一批变更的同时输出事件（`Event`）：

- 右侧插入使计数 `0 -> 1`：该键的所有左侧行输出 `leave`；
- 右侧删除使计数 `1 -> 0`：该键的所有左侧行输出 `enter`；
- 计数只在正值之间变化（如 `1 -> 2`、`2 -> 1`）：**不输出**任何事件；
- 插入左侧行：若其为空值键或对应右侧计数为 0，输出 `enter`，否则静默；
- 删除左侧行：若它当时在结果中，输出 `leave`，否则静默；
- 同一批提交产生的多条输出按左侧行标识升序排列；
- 同一标识在一批内互相抵消的翻转（如计数 `0 -> 1 -> 0`）净输出为空。

已提交日志可用 `Log()` 获取；`ApplyLog(events)` 将日志应用到空成员集合。
**任意前缀**的变更日志应用后的视图，都等于该时刻的批量重算结果
（`Recompute()`，不依赖任何增量状态）。测试中在每次提交后设置检查点，
重放每个前缀并与快照、批量重算比对。

### 边界与错误类别

整批提交先整体校验、后应用：任一条非法则整批拒绝，两侧行、右侧计数、
视图成员与已输出日志都不改变（失败不留痕）。可区分的拒绝原因
（`RejectedError.Reason`，互不相同）为：

- `ReasonEmptyID`：行标识为空字符串（插入与删除均拒绝）；
- `ReasonDuplicateInsert`：同侧重复插入已存在标识（含同批先插后插）；
- `ReasonDeleteMissing`：删除同侧不存在的标识（含同批先删后删）；
- `ReasonTooManyRows`：提交后某侧行数超过构造时给定上限
  （`NewView(maxRows)`，`maxRows<=0` 使用 `DefaultMaxRows`）；
- `ReasonInvalidSide` / `ReasonInvalidOp`：未知的一侧或操作类型（防御性）。

其他边界约定：

- 标识的作用域是“同侧”：左右两侧可以使用相同标识；
- 删除时不检查 `Change.Key`，以被删除行当前保存的键为准；
- 同批“先删后插”同一标识是合法重写，不视为重复插入；
- 行数上限按侧分别计算，超限检测考虑整批内的净增减。

### 并发

`View` 内部使用读写锁：多个执行体可并发调用 `Commit`、`Has`、
`Snapshot`、`Recompute`、`SelfCheck` 与 `Log`；提交串行化，
自检与快照可与提交并发，看到的始终是某个一致提交点上的状态。

### 本地验证

```bash
# 全部测试（含逐步输入/输出/判定依据日志，go test -v 可见）
go test -v ./ontology

# 竞态检测
go test -race ./...

# 自检、格式与 vet
gofmt -l .
go vet ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

测试覆盖：计数穿越零（`0->1`、`1->2`、`2->1`、`1->0` 及批内抵消）、
空值语义（空值左右行互不匹配）、右侧先到、每一类非法输入、
拒绝后状态不变、批量重算一致性、日志任意前缀一致性，以及多执行体
并发提交与自检。
