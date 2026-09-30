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

## 分区提交触发器（`partition` 包）

`partition/trigger.go` 实现按事件时间分区的提交触发器：`Options{Writers: W, Period: P, AllowedRetries: R, Lateness}` 构造，全部操作可并发调用（内部互斥，效果等价于某一串行顺序）。

### 分区与就绪判定

- 分区 `k` 覆盖事件时间 `[k*P, (k+1)*P)`，事件时间 `t` 落在分区 `t / P`；首次写入创建分区并计数。
- 每个写入子任务（共 `W` 个，编号 `[0,W)`）维护单调不减水位；子任务可结束（`Finish`），结束后水位视为正无穷（`Infinity`），结束不可重复。
- 全局水位：所有 `W` 个子任务都至少上报过一次（水位上报或结束）后才存在，值为各子任务水位最小值；此前为「不存在」。
- 分区就绪当且仅当全局水位 `>= (k+1)*P + Lateness`，边界取等号即就绪。

### 提交规则

- 首轮提交固定两步、严格次序：① 登记元数据（`StepRegisterMetadata=0`）② 写成功标记（`StepSuccessMarker=1`）。
- 外部通过 `ReportStep(id, step, success)` 对当前唯一可执行步骤上报结果。成功推进一步；失败则该步失败次数加一，同一步可重试；累计达 `R` 次分区转 `commit_failed`，只能经 `Reset` 人工恢复。
- `Reset`：回到该轮第一步、失败次数清零。首轮失败回到 `ready`（从登记元数据重来）；补提交轮失败回到 `pending_patch`（只补写成功标记）。版本号保留不变。
- 每完整完成一轮提交（两步全成功，或补提交一轮成功），提交版本 `Version` 加一。
- 任一时刻每个分区至多一个可执行步骤，由 `ExecutableStep` 或上报结果给出。

### 补提交（迟到写入）

- 已提交（`committed`）分区再收到写入：计数增加并转 `pending_patch`，该轮只有「写成功标记」一步。
- 补提交完成前的多次写入合并到同一轮，只增加计数，不开新一轮。
- 分区尚未提交完成（`in_progress` / `commit_failed` 等）时收到写入：只增加计数，状态不变。

### 阻塞次序

- 分区第一步（登记元数据）只能在所有编号更小的**已创建**分区均处于 `committed` 或 `pending_patch`（均代表首轮已提交完成）时开始。
- 更小分区处于进行中或提交失败会阻塞；重置并提交完成后自动放行。因此每个分区第一步开始时，所有更小的已创建分区都已提交，首轮提交严格按分区号升序。补提交只写标记，不受更小分区阻塞。

### 拒绝原因（按所列次序判定，错误可区分，被拒操作不改状态）

- 写入 `Write`：子任务号越界、事件时间为负、子任务已结束。
- 水位上报 `AdvanceWatermark`：子任务号越界、子任务已结束、水位回退。
- 结束 `Finish`：子任务号越界、重复结束。
- 步骤上报 `ReportStep`：分区不存在、当前不可执行（未就绪 `ErrNotReady`、被阻塞 `ErrBlocked`、已提交 `ErrAlreadyCommitted`、提交失败 `ErrCommitFailed`）、步骤不符 `ErrWrongStep`。
- 重置 `Reset`：分区不存在、分区不在提交失败。

### 日志

传入 `Options.Log`（实现 `Printf`，例如 `log.New(...)` 的结果）后，每个操作都会打印**输入、输出与判定依据**（如 `threshold=`、`global=`、`reason=blocked`、状态迁移与版本变化）；不传则静默。

### 本地验证

```bash
go test -race -v ./partition
go test -cover ./partition
```

测试覆盖：水位恰等于就绪边界（含延迟参数）、结束使水位跳到正无穷、较小分区提交失败阻塞较大分区、已提交后迟到写入只补写成功标记且多次写入合并、补提交失败后重置、全部拒绝原因与判定次序、并发写入/上报水位不丢不重。
