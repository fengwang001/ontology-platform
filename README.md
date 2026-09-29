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

## 复杂事件模式匹配（`cep` 包）

`cep.Matcher` 在同一键的事件流中找出“先事件（`FirstType`）-> 后事件（`SecondType`）”的配对。

### 窗口条件

- 匹配只发生在**同一个键**的事件之间；同一键的时间戳必须单调不减。
- 配对要求 `0 <= Second.Timestamp - First.Timestamp <= Window`，窗口为**闭区间**，
  时间差恰好等于上限也算命中。

### 连续模式

- **宽松连续（`RelaxedContiguity`）**：先事件进入该键的待匹配队列，先事件与后事件
  之间允许夹杂任意事件；一个先事件至多配对一个后事件，后事件到达时消费队列中
  所有仍在窗口内的先事件，窗口外的先事件随时间推进自动过期。
- **严格连续（`StrictContiguity`）**：后事件必须是同一键内紧挨先事件的下一个事件；
  同键的任意其他事件都会打断连续，其他键的事件互不影响。

### 拒绝规则

以下情况整批拒绝，且被拒绝的批不会改变待匹配队列、上一事件或已输出的配对
（批处理为克隆-提交的原子语义）。错误为可区分的哨兵错误，可用 `errors.Is` 判定：

- `ErrInvalidWindow` / `ErrInvalidMaxPending` / `ErrEmptyEventType` /
  `ErrSameEventType` / `ErrInvalidMode`：构造参数非法。
- `ErrEmptyKey` / `ErrEmptyType`：事件键或类型为空。
- `ErrNonMonotonicTime`：同一键时间戳倒退。
- `ErrPendingLimitExceeded`：某键待匹配先事件队列超过 `MaxPending`。

### 并发与确定性

所有方法可在多 goroutine 下并发调用；`Matches()` 返回配对副本，可安全并发读取。
同一输入序列反复计算得到完全相同的输出。日志（`slog`）会打印输入事件、
配对结果与判定依据（配对、过期、拒绝原因）。

### 本地验证

```bash
go test -race -v ./cep/
```
