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

## 保存点恢复准入器（`savepoint` 包）

`savepoint.Gatekeeper` 在新作业图与保存点之间按算子标识与来源映射匹配状态、
判定兼容性，保证恢复要么完整可用、要么零副作用地被拒绝。登记、恢复、停止、
删除均可并发调用：同一作业标识并发恢复只成功一个；被存活作业引用的保存点
不会被并发删除；相同输入得到完全相同的计划与丢弃清单。

### 承接规则

- 保存点登记后不可变，含各算子的标识、最大并行度 `m` 与若干具名状态项
  （种类为 `value`/`list`/`map`，值类型为 `int`/`long`/`string`）。
- 新作业图中的算子通过可选的**来源标识**（`SourceID`）声明承接哪个保存点算子；
  未声明时承接与自身同名的保存点算子；两者都没有则为**新增算子**，以空状态启动，
  最大并行度缺省 `128`。
- 承接者的最大并行度缺省继承保存点的 `m`，且必须等于 `m`（显式声明不一致即拒绝）。
- 保存点中未被承接的算子、被承接算子中新图缺失的同名状态项，属于**丢弃**：
  仅在允许丢弃（`allowDiscard=true`）时可恢复，并分别记入计划的
  `DroppedOperators` 与 `DroppedStateItems`（`保存点算子.状态项` 形式）。
- 新图中新增的状态项以空状态启动，不影响恢复。

### 兼容规则与恢复动作

- 同名状态项：种类必须相同；值类型相同，或由 `int` 变宽为 `long`，其余（含
  `long` 变窄为 `int`）均不兼容。
- 成功时每个算子的动作（`OperatorPlan.Action`）：
  - `restore-direct`：承接保存点算子且无需迁移；
  - `restore-widen`：存在 `int`→`long` 变宽状态项（列于 `WidenedItems`），迁移后恢复；
  - `start-empty`：新增算子，空状态启动。
- 恢复成功后作业标识即被占用；保存点被存活作业引用时不能删除，
  调用 `Stop` 停止作业后才可删除。

### 拒绝次序

拒绝时只报告次序最靠前的第一类原因，同类对象全部升序列出于 `Rejection.Objects`：

1. `savepoint-not-found`：保存点不存在；
2. `job-id-occupied`：作业标识已被占用；
3. `invalid-job-graph`：算子标识为空或重复、`p` 非正或大于生效最大并行度、
   声明的来源不在保存点内；
4. `duplicate-claim`：同一保存点算子被多个新算子承接（同名与来源映射撞车也算）；
5. `max-parallelism-mismatch`：承接者最大并行度与保存点的 `m` 不一致；
6. `unclaimed-operators`：未允许丢弃时存在未被承接的保存点算子；
7. `incompatible-state-items`：状态项种类不同或值类型不兼容（`算子.状态项`）；
8. `missing-state-items`：未允许丢弃时新图缺失保存点状态项（`保存点算子.状态项`）。

被拒绝的操作不改变任何内部状态（不占用作业标识、不建立引用、不删除保存点）。
所有操作均在日志中打印输入、输出与判定依据。

### 本地验证

```bash
# 全量测试（含竞态检测与详细日志）
go test -race -v ./savepoint/

# 代码检查
gofmt -l .
go vet ./...
```
