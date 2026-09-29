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

## 时间线时间点恢复（`pitr` 包）

`pitr` 包实现带时间线分叉（timeline branching）的时间点恢复：依据基础备份、
归档日志段与时间线历史，为目标时间线上的目标位置选出基础备份与回放计划并执行
恢复，恢复结果与在该时间线上直接执行到目标位置的状态一致。

### 核心模型

- 位置 `Position` 是统一数轴上的整数坐标；日志段 `Segment` 属于某条时间线，
  覆盖左闭右开区间 `[Start, End)`，每个位置只回放一次。
- 时间线 `Timeline` 记录父时间线 `Parent` 与分叉位置 `Fork`：分叉位置之前的
  历史取自父时间线，分叉位置起取自身。根时间线为 `1`，`Fork=0`。
- 基础备份 `Backup` 记录所属时间线 `TLI` 与已含位置上界 `Upper`（不含）。

### 生效时间线的推导

规划时先沿 `Parent` 指针从目标时间线回溯到根，再由根向目标排列成祖先链。链上
每条时间线在一个半开窗口内生效：

- 窗口下界为该时间线自身的 `Fork`；
- 窗口上界为祖先链中更靠近目标的下一条时间线的 `Fork`；
- 目标时间线本身没有上界。

因此回放位置 `p` 时，只能使用祖先链上唯一满足 `lo <= p < hi` 的时间线所归档的
段。归档段会先按该窗口裁剪：分叉后的旧时间线段不能服务子时间线，落在段中间的
分叉会把同一个段裁成分属两条时间线的两个回放步骤。

回放终点 `End` 由目标模式决定：

- 不含目标位置（exclusive）：`End = Target`；
- 含目标位置（inclusive）：`End = Target + 1`。

### 备份可用条件

基础备份在且仅在同时满足以下条件时可用：

1. 所属时间线位于目标时间线的祖先链上；
2. `Upper` 落在该时间线相对目标链的生效窗口内（下界 `<=`，上界 `<=`）；
3. `Upper` 不超过回放终点 `End`。

在所有可用备份中取 `Upper` 最大者；`Upper` 并列时取时间线编号更大者（例如分叉
边界位置上父、子两个备份并列时取子时间线备份）。

### 规划判定顺序

`PlanRecovery` 不修改任何登记，按以下固定顺序只报第一个原因：

1. 目标时间线不存在：`ErrTimelineNotFound`；
2. 目标超出已归档末端（祖先链各裁剪段末端的最大值）：
   `ErrTargetBeyondArchive`；
3. 无可用基础备份：`ErrNoBackup`；
4. 备份上界到回放终点之间日志有缺口：返回包装 `ErrGap` 的 `GapError`，
   `GapError.Position` 为第一个缺口位置。

登记时间线时，若其分叉位置早于父时间线自身的分叉位置，拒绝并返回
`ErrInvalidFork`；父时间线不存在、编号非法、段区间非法或重叠、备份非法等同样被
拒绝。任何被拒绝的操作都不会新建时间线，也不会改变既有登记。

### 新时间线与分叉位置

`Recover` 在规划通过后执行恢复：恢复基础备份前缀，再按计划顺序回放各裁剪段，
并校验结果状态与 `DirectState`（在目标时间线上直接执行到 `End`）一致。恢复成功
后新建一条时间线：

- 编号为当前最大编号加一；
- 父时间线为目标时间线；
- 分叉位置为第一个未回放的位置，即回放终点 `End`
  （exclusive 目标为 `Target`，inclusive 目标为 `Target+1`）。

归档、规划与恢复均可并发调用：目录由读写锁保护，恢复在写锁内重新校验并分配
编号，因此并发恢复得到的新时间线编号互不相同且连续；相同输入反复规划得到逐项
相同的计划。每个关键动作通过 `Logger` 打印输入、输出与判定依据（默认输出到
stderr，可用 `WithLogger` 替换为测试日志）。

### 本地验证

```bash
# 全量测试（含三层分叉、段中分叉、旧分支段不可用、两种目标模式边界、
# 备份并列、缺口定位、拒绝不落地、并发编号连续与计划确定性）
go test -race -v ./pitr

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 格式与静态检查
gofmt -l .
go vet ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
