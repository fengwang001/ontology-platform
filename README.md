# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 异步输出缓冲（`asyncbuf`）

`asyncbuf` 实现异步输入输出算子的**有序**与**无序**输出缓冲。它接收
**元素**与**水位线**交织的输入流；元素的异步请求何时完成由调用方通过
`Complete(id)` 声明，缓冲据此决定哪些条目可以输出。

### 两种输出模式

- **Ordered（有序）**：输出顺序严格等于输入顺序。仅当**队头**是
  “已完成元素”或“水位线”时才输出；队头是未完成元素则阻塞，后续即使
  已完成也一律等待（队头阻塞，严格保序）。
- **Unordered（无序）**：相邻水位线把元素划分为若干**段**。只有当前
  **已开放段**内的元素完成后才立即输出（段内按完成先后，不按输入序）；
  **未开放段**内已完成的元素被**扣住**。水位线在其之前全部元素输出后
  立即输出，并**级联**开放后续段、按完成先后释放被扣元素。

### 水位线屏障

水位线是一道屏障：它必须等待**其之前**的所有元素都输出后才能输出。
有序模式下它与普通条目一样参与队头判定；无序模式下它是段边界——
只有前一段排空、水位线输出后，下一段才开放。

### 占用与容量

- `capacity` 为容量上限（构造时指定，>=1）。
- `occupied` 为当前**已提交但尚未输出**的元素数；提交元素 +1，元素输出 -1。
  水位线不占用容量。
- 提交元素时若 `occupied == capacity`，整体拒绝（`ErrCapacityFull`）。

### 边界与错误类别

所有非法输入**整体拒绝且不留痕**（队列、占用数、已产生输出均不变）。
每种原因对应一个可用 `errors.Is` 区分的类别（`asyncbuf.Error.Kind`）：

| 类别 | 触发条件 |
| --- | --- |
| `ErrCapacityFull` | 提交元素时占用已达容量上限 |
| `ErrEmptyID` | 提交元素的标识为空 |
| `ErrDuplicateID` | 提交元素的标识与缓冲中已有元素重复 |
| `ErrUnknownID` | 完成声明指向不存在（或已输出被遗忘）的标识 |
| `ErrAlreadyCompleted` | 对仍在队中、已完成的元素重复声明完成 |
| `ErrWatermarkNotMonotonic` | 水位线未相对上一条严格递增 |
| `ErrInvalidArgument` | 构造参数非法（容量或模式） |

> 注意：元素一旦输出，其标识即从缓冲移除；此后再 `Complete` 该标识会
> 得到 `ErrUnknownID` 而非 `ErrAlreadyCompleted`。

### 并发

`Submit` / `SubmitWatermark` / `Complete` / `Drain` 及各类查询均可被多个
goroutine 并发调用，内部由互斥锁保护。

### 本地验证

```bash
# 该包全部测试（含竞态检测）
go test -race -v ./asyncbuf

# 仅跑“与朴素模型逐条对照”的随机序列测试
go test ./asyncbuf -run 'TestRandomMatchesNaive'
```

测试用一个独立的**朴素不动点模型**（`naive_test.go`）作为参照：对同一
随机操作序列，逐步比对生产实现与朴素模型的输出、拒绝类别与占用数，
确保两种模式下的结果与朴素判定**逐条一致且可复现**（固定随机种子）。

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
