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

## 滑动窗口最大值组件（`slidingwindow`）

`slidingwindow` 包提供固定容量的滑动窗口，并在元素进入与逐出时实时维护窗口内最大值。

### 窗口与逐出规则

- 容量在 `New(capacity)` 时固定，`capacity < 1` 返回 `ErrInvalidCapacity`。
- `Push(v)` 追加元素；窗口已满时先自动逐出最早元素，返回值会告知被逐出的值。
- `Evict()` 显式逐出并返回最早元素；空窗口返回 `ErrEmptyEvict`。
- `PushAll(vs)` 批量追加，语义等价于顺序 `Push`（含满窗自动逐出）；
  批次为空返回 `ErrEmptyBatch`，任一值为 `NaN`/`+Inf`/`-Inf` 返回 `ErrInvalidValue`
  且整批不生效。
- 任何被拒绝的操作都不会改变窗口内容与搬运计数。

### 最大值维护规则

- 窗口元素存放在环形缓冲中；另维护一个“索引单调双端队列”，
  其对应值严格单调递减，**队首始终是窗口最大值**，`Max()` 为 O(1)，逐出时不重扫整窗。
- 新元素入窗时从队尾弹出所有“不大于”它的元素后入队：
  并列时用更新的元素替换旧元素，因此并列最大值被逐出后最大值仍正确。
- 元素离窗时，若它正是队首则队首出队；非队首元素此前已被更大的新元素弹出。
- 每个元素在其生命周期内至多进入单调队列一次（摊还 O(1)），
  累计搬运次数可通过 `Moves()` 查看，且不超过成功写入的元素总数。
- `Snapshot()` 在同一把读锁下返回窗口内容副本与最大值，
  因此并发读到的最大值与对同一份快照逐一扫描的结果必然一致；
  空窗口返回 `ErrEmptyMax`。组件无后台状态，同一输入序列反复计算输出完全相同。

错误均为哨兵错误，可用 `errors.Is` 区分：`ErrInvalidCapacity`、`ErrEmptyEvict`、
`ErrEmptyMax`、`ErrInvalidValue`、`ErrEmptyBatch`。

### 本地验证

```bash
# 全量测试（带竞态检测与详细日志：输入、逐出值、窗口、最大值及判定依据）
go test -race -v ./slidingwindow/

# 覆盖率（当前为 100%）
go test -cover ./slidingwindow/

# 格式与静态检查
gofmt -l .
go vet ./...
```

测试覆盖：并列最大值被逐个逐出、单调递减段、混合序列每步
“队首最大值 == 逐一扫描结果”、容量/空窗/非法值/非法批次等拒绝路径及其无副作用、
批量写入原子性、搬运次数上界、并发读快照一致性（`-race`）与重复运行确定性。
