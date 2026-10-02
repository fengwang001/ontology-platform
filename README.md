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

## 存储配额管理器（`quota` 包）

`quota.Manager` 实现用户与组两级、带软/硬限额与宽限计时的存储配额管理。
构造：`quota.New(Gu, Gg)`，用户宽限 `Gu` 与组宽限 `Gg` 均为 `[1, 10^9]`，
越界时以参数非法整体拒绝（返回带 `quota.ReasonInvalidArgument` 的
`*quota.RejectError`）。所有实体状态由单个读写锁保护，操作串行化执行，
并发调用结果等价于某个串行顺序。

### 实体与不变量

- `AddGroup(g, soft, hard)`、`AddUser(u, g, soft, hard)` 登记实体；编号
  `[0, 10^6]`，`0 <= soft <= hard <= 10^15`，初始用量与宽限起点均为空。
- 用户必须属于一个已存在的组；编号已存在报 `ILLEGAL_STATE`，所属组不
  存在报 `NOT_FOUND`。
- 组用量恒等于其用户用量之和；用量非负；用量 `<= soft` 时宽限起点必为
  空，`> soft` 时起点必非空，且起点不晚于已接受的最大 `now`。
- `Usage(id)` / `Grace(id)` 只检查实体是否存在；同名编号（既是用户又是
  组）解析为用户，可用 `UserUsage/GroupUsage`、`UserGrace/GroupGrace`
  显式选择层级。

### 统一的计时整理规则

每个被接受的操作结束时，对被它改变过用量或限额的每个实体整理一次：

1. 用量 `<= soft`（恰等于软限不算超软限）：宽限起点置为无；
2. 否则起点为无时置为当前 `now`；已有起点时保持不变。

实体「宽限已过」指起点非无且 `now >= 起点 + 该层宽限`（用户用 `Gu`，
组用 `Gg`）。恰等于 `起点 + 宽限` 即已过，少 1 未过。

### 各操作的检查顺序

`now` 必须不小于已接受的最大 `now`（登记操作不接受 `now`）。

- `Alloc(u, x, now)`：用户硬限（`usage+x > hard` 即拒，恰等于硬限通过）
  → 用户宽限已过 → 组硬限 → 组宽限已过；全部通过后用户与组用量各加
  `x`，两个实体各整理一次。
- `Free(u, x, now)`：不检查限额与宽限，但 `x` 不得超过用户用量
  （超释放为状态错误）；用户与组用量各减 `x` 后各整理一次，因此宽限
  已过后仍允许释放。
- `SetLimits(kind, id, soft, hard, now)`：限额修改后允许用量高于新硬限
  （此后任何分配都会被硬限拒绝）；用新限额对该实体当场整理一次。
- `Move(u, g2, now)`：目标组等同于大小为用户当前用量 `U` 的分配——先
  查目标组硬限，再在 `U > 0` 时查目标组宽限（`U == 0` 跳过宽限检查）；
  源组等同于一次释放，用户自身（用量/限额/宽限）不变；成功后源组与
  目标组各整理一次。目标组与当前组相同为状态错误。

### 拒绝原因与优先级

所有失败只报按下列顺序遇到的第一个原因，且不改变任何用量、限额、宽限
起点与已接受的最大 `now`（时钟回退也不会推进时钟）：

1. `ReasonInvalidArgument` 参数非法（构造与登记参数越界、`soft > hard`）
2. `ReasonClockRollback` 时钟回退
3. `ReasonNotFound` 实体不存在（用户/目标组/被修改实体）
4. `ReasonIllegalState` 状态错误（Free 超释放、Move 目标组相同、
   登记时编号已存在）
5. `ReasonUserHardLimit` 用户硬限
6. `ReasonUserGraceExpired` 用户宽限已过
7. `ReasonGroupHardLimit` 组硬限（Move 中为目标组硬限）
8. `ReasonGroupGraceExpired` 组宽限已过（Move 中为目标组宽限已过）

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race -count=1 ./...

# 定向边界用例 + 规范示例回放
go test -race -v -run 'TestSpecExample|TestSoftHardBoundaries|TestGraceExactBoundary|TestMove|TestSetLimits|TestRejected|TestConcurrent' ./quota

# 2000 组随机序列与朴素模拟器对照（-v 打印每步输入、两侧输出与判定依据）
go test -race -v -run TestRandomDifferential ./quota

gofmt -l .
go vet ./...
```

随机对照（`quota/diff_test.go`）用固定种子生成 2000 组序列，对每个操作
比较真实实现与独立编写的朴素模拟器（`quota/naive_test.go`）的拒绝原因，
并在每步后逐实体快照对比用量/限额/宽限起点，同时校验「组用量等于用户
用量之和」「用量与宽限起点一致性」「起点不晚于最大 now」等不变量；
相同序列重放结果完全一致。
