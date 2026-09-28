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

## 稀疏位点索引（`segment` 包）

`segment/segment.go` 实现了一个位点可能存在空洞的只追加日志段，并用稀疏索引支持按目标位点快速定位记录。

### 数据模型

- 记录按位点（offset）严格递增追加，每条记录占用 `size > 0` 字节。
- 段内物理位置（position）为之前所有记录字节数之和，首条为 `0`。
- 索引条目保存 `{相对位点 int32, 物理位置 int64}`；相对位点 = `offset - baseOffset`，只允许落在 `int32` 范围内，超出即拒绝。

### 索引建立规则（追加时）

- 每累计 `intervalBytes` 字节记录一个索引条目，`intervalBytes` 必须为正（`NewSegment(base, interval)`）。
- 顺序固定为“**先判定、后写入**”：追加一条记录前，若自上个条目以来累计字节 `>= intervalBytes`，先按**写入前**的累计值写入条目 `{相对位点, 当前物理位置}`，再写入记录并累加字节。
- 首条记录前累计为 0，因此首条不会建条目；条目相对位点与物理位置都严格递增。
- 可用 `segment.ExpectedEntries(records, base, interval)` 从记录重算索引进行校验。

### 查找规则

- 先在稀疏索引中二分找到“不超过目标相对位点”的**最后一个**条目作为起点（没有则从段首开始）。
- 从该条目的物理位置开始顺序扫描，返回第一条 `offset >= target` 的记录。
- 结果与从段首逐条朴素扫描完全一致（测试以朴素扫描为基准逐条比对）。

### 边界与错误类别

所有错误均为包级哨兵错误，可通过 `errors.Is` 区分；每次拒绝都是整体失败，不写入记录、不追加索引、不改变字节数（失败不留痕，被拒的追加也不会“消耗”累计字节）：

- `ErrInvalidRecordSize`：记录字节数非正，或建段时索引间隔非正。
- `ErrNonMonotonicOffset`：追加位点不严格大于上一条位点。
- `ErrRelativeOffsetOverflow`：相对位点超出 `int32`（`MaxInt32`）范围。
- `ErrOffsetBelowBase`：追加或查找的位点低于基位点。
- `ErrRecordNotFound`：查找时不存在位点不小于目标的记录。

### 并发

`Segment` 内部用读写锁保护，`Append` 与 `Lookup` 可被多个 goroutine 并发调用；测试以 `-race` 运行多写者追加（竞争失败者按 `ErrNonMonotonicOffset` 重试）与多读者查找，并在同一读锁快照内核对索引查找与朴素扫描一致。

### 本地验证

```bash
# 详细输出（每条追加/查找的输入、索引内容与判定依据都会打印到日志）
go test -race -v ./segment/

# 重复执行与全量测试
go test -race -count=3 ./segment/
go test ./...

# 格式与静态检查
gofmt -l .
go vet ./...
```
