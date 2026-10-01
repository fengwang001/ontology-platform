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

## 窗口帧边界计算器（`frame` 包）

`frame/frame.go` 在按 int64 排序键非降序追加的分区上计算窗口函数的帧边界。

- `frame.New(capacity)` 创建有容量上限的分区；`Append(key)` 追加一行（下标从 0 起），键相等的行构成一个并列组，组号从 0 起；`Frame(i, spec)` 返回第 `i` 行的帧，表示为升序、互不相邻的非空半开区间 `[From, To)` 序列，空帧返回空序列。

### 三种差值度量

第 `j` 行是否属于第 `i` 行的帧，取决于差值 `d`，三种模式的唯一差别是 `d` 的度量方式：

- `ROWS`：`d = j - i`（行下标差）。
- `RANGE`：`d = key_j - key_i`（排序键差，内部用 `math/big` 精确计算；即使真实差值超出 int64 范围，也不会因回绕或饱和改变比较结论）。
- `GROUPS`：`d = g_j - g_i`（并列组编号差）。

### 起点与终点条件

界的类型按强弱次序为：`UNBOUNDED PRECEDING` < `n PRECEDING` < `CURRENT ROW` < `n FOLLOWING` < `UNBOUNDED FOLLOWING`，其中 `n` 为非负 int64，只有 `n PRECEDING` / `n FOLLOWING` 使用 `n`。

- 起点条件：`UNBOUNDED PRECEDING` 恒真；`n PRECEDING` 为 `d >= -n`；`CURRENT ROW` 为 `d >= 0`；`n FOLLOWING` 为 `d >= n`。
- 终点条件：`n PRECEDING` 为 `d <= -n`；`CURRENT ROW` 为 `d <= 0`；`n FOLLOWING` 为 `d <= n`；`UNBOUNDED FOLLOWING` 恒真。
- 一行在排除前的帧中，当且仅当同时满足起点条件与终点条件。
- 注意 `CURRENT ROW` 在 `ROWS` 下不含前置并列行，而在 `RANGE`/`GROUPS` 下包含整个并列组。

### 排除项

排除项作用于排除前的帧，可选 `NONE`、`CURRENT ROW`、`GROUP`、`TIES`：

- `CURRENT ROW`：去掉第 `i` 行。
- `GROUP`：去掉第 `i` 行所在并列组的全部行（含第 `i` 行）。
- `TIES`：去掉该并列组中除第 `i` 行以外的行；第 `i` 行若不在帧内也不会被补入。

排除后相邻的存活行会合并为半开区间，因此帧可能分裂成多段。

### 错误（按此顺序只报第一个，被拒绝操作不改变分区）

- `New`：容量不为正 → `ErrInvalidCapacity`。
- `Append`：容量已满 → `ErrCapacityFull`；键小于最后一行的键 → `ErrKeyOutOfOrder`。
- `Frame`：模式、界类型或排除项非法 → `ErrInvalidSpec`；起点为 `UNBOUNDED FOLLOWING`、终点为 `UNBOUNDED PRECEDING` 或起点强于终点（同类型可以）→ `ErrInvalidBoundPair`；偏移界的 `n` 为负 → `ErrNegativeOffset`；`i` 越界 → `ErrRowOutOfRange`。

`Append` 与 `Frame` 由读写锁保护，可并发调用且结果等价于某个串行顺序；`Frame` 返回的切片独立分配，不与内部存储共享。

### 本地验证

```bash
# 全量测试（含 2000 组随机分区/spec 对拍，-v 打印输入、输出与判定依据）
go test -v ./frame

# 竞态检测与重复执行
go test -race -count=3 ./frame

# 格式与静态检查
gofmt -l .
go vet ./frame
```

测试中的 `naiveFrame` 按题目条件逐行朴素判定（RANGE 同样使用 `math/big`），与实现对拍；随机种子固定（`20261001`），相同操作序列重放结果完全一致。
