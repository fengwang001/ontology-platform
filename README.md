# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## tombstone：范围删除墓碑碎片化器

`tombstone` 包登记带序号的半开范围墓碑并维护唯一的碎片化表示，用于点记录的被覆盖判定。

### 数据模型

- 键为字节串，按字节序（`bytes.Compare`）比较。
- 墓碑 `(s, e, q)` 表示键范围 `[s, e)` 与正整数序号 `q`；可乱序、可重复登记（完全相同的墓碑重复登记也各占一个墓碑计数），不同墓碑可同序号。
- 容量上限 `Cap` 通过 `tombstone.New(cap)` 指定。

### 碎片切分与合并规则

- 取全部墓碑端点排序去重，相邻端点切成互不重叠的半开段。
- 每段记录覆盖它的墓碑序号集合：按序号去重后降序排列。
- 无任何墓碑覆盖的段不产生碎片。
- 相邻且序号集合完全相同的碎片合并为一段；嵌套导致集合不同的段不合并。
- 碎片表由墓碑集合唯一确定，与登记顺序无关（同一墓碑集合任意顺序登记得到逐字段相同的碎片表）。

### 覆盖判定

`Covered(k, q, snap)`：点记录键为 `k`、序号为 `q`、快照号为 `snap`，当且仅当存在覆盖 `k` 的墓碑序号 `t` 满足 `q < t <= snap` 时为真。`t == q` 不覆盖，`t == snap` 可见；`snap < q` 时恒为假。判定对碎片表做二分查找，考察的碎片数为对数级（由非导出计数器 `examined` 记录，测试在 1000 与 100000 个碎片下对比验证）。

### 登记拒绝

登记按以下顺序校验，只报告第一个可区分的原因，被拒绝的操作不改变碎片表与墓碑计数：

1. `ErrInvalidRange`：`s` 不小于 `e`；
2. `ErrZeroSeq`：序号为 0；
3. `ErrInvalidCap`：`Cap` 非正（非正在构造）；
4. `ErrCapExceeded`：墓碑总数已达 `Cap`。

登记、判定与碎片查询可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race ./tombstone/

# 查看随机对拍日志（2000 组随机输入 vs 逐条扫描朴素判定，含输入、输出与判定依据）
go test -v -run TestRandomVsNaive ./tombstone/

# 查看碎片考察计数器对比（1000 vs 100000 个碎片）
go test -v -run TestCoveredExaminedDoesNotGrowLinearly ./tombstone/
```

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
