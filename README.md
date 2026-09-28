# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 日志段稀疏位点索引（`logseg` 包）

在只追加的日志段上，按逻辑位点（position，允许存在空洞）快速定位记录：

- 记录按位点**严格递增**追加；每条记录占用正数字节数，段内物理位置 = 之前所有记录字节数之和。
- 每累计达到索引间隔（`indexIntervalBytes`，构造时指定）个字节，就在**写入该记录之前**记录一个稀疏索引条目 `{RelPos, PhysOff}`；段内第一条记录必然被索引。判定为累计字节数 `>= 间隔`（恰好达到也触发）。
- `RelPos = Position - BasePosition`，以 `int32` 受限整数存储，超出 `[0, MaxInt32]` 即拒绝追加。

### 查找规则

1. 目标位点低于 `BasePosition` 直接拒绝（`ErrBelowBasePosition`）。
2. 在索引中二分定位**不超过目标的最后一个条目**；若目标早于首个索引条目则从段首开始。
3. 从该条目指向的记录起顺序扫描，返回第一条位点 `>= 目标` 的记录；扫到段尾未命中返回 `ErrNotFound`。
4. 结果与从段首逐条朴素扫描完全一致，且索引的相对位点、物理位置均严格递增，并可由记录序列按规则原样重算。

### 错误类别（全部互不相同，用 `errors.Is` 区分）

- `ErrInvalidInterval`：构造时间隔非正。
- `ErrInvalidBasePosition`：构造时基位点为负。
- `ErrInvalidSize`：追加记录字节数非正。
- `ErrNonMonotonicPosition`：位点不严格递增（相等或回退）。
- `ErrPositionOverflow`：相对位点超出 `int32` 范围。
- `ErrBelowBasePosition`：查找目标低于基位点。
- `ErrNotFound`：段为空或不存在位点不小于目标的记录。

任何拒绝都发生在任何状态变更之前：记录、索引、字节数均保持不变（失败不留痕）。追加与查找均通过读写锁支持多执行体并发调用。

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

# 仅验证稀疏位点索引包（建议始终带竞态检测）
go test -race -v ./logseg
go test -race -count=10 ./logseg

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
