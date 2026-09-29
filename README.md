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

## 日志结构存储与段清理器（`logstore` 包）

`logstore` 实现只追加写入的定长段存储：写与删除都作为块追加到当前段，
段满封存；段槽总数有上限（`Config.MaxSegments`），槽位不足时触发清理。

### 代价收益挑选公式

对已封存段计算分数，取最大者作为牺牲段：

```
score = (1 - u) * age / (1 + u)
u    = liveBytes / segmentSize        （存活率）
age  = clock - segment.maxTS          （逻辑时钟 - 段内最新块写入时刻）
```

即 `(segmentSize - liveBytes) * age / (segmentSize + liveBytes)`，用
`math/big.Rat` 做精确有理数比较，不用浮点；分数并列时取段号最小者。
存活率为零的段不搬迁、直接回收。清理决策（每个候选的存活字节、年龄、
分数与最终牺牲段）通过 `Config.Logger` 输出。

### 存活判定

- 数据块：当且仅当索引指向它（它是该键最新一次操作）时存活；被覆盖的
  旧块立即失效。
- 墓碑块：当且仅当索引指向它 **且** 任一其他段中还留有同键更旧的块时
  存活。

### 墓碑保留推导

删除只追加墓碑，旧数据块仍物理留在原段中。若此时清理墓碑所在段并丢弃
墓碑，索引将失去该键的删除记录，而旧块仍在盘上——删除语义丢失。因此只
要任一其他段留有该键更旧的块，墓碑就必须视为存活并随搬迁保留（按原写
入顺序、保留原写入时刻）。当最后一个更旧块随其段被回收后，墓碑不再阻
止任何旧值复活，转为死亡，下次清理其所在段时随垃圾一起丢弃，索引条目
同时移除。

### 账目不变式

任意时刻、任意段：

```
段存活字节 = 索引指向该段的数据块字节 + 该段存活墓碑字节
```

`Store.VerifyAccounting` 逐段校验该等式；测试在每次操作后断言。

### 空间耗尽条件

写入需要新段而无空闲槽时先清理一个牺牲段：存活块迁入日志尾（当前段）
后净增一个空闲槽才执行；若牺牲段存活字节放不进当前段剩余空间（搬迁还
要再消耗一个槽，净增为零），或根本没有已封存段，则整个写入以
`ErrSpaceExhausted` 拒绝，不追加任何块。其它可区分的拒绝原因：
`ErrEmptyKey`（空键）、`ErrBlockTooLarge`（单块超过段大小）、
`ErrKeyNotFound`（删除不存在的键）。

### 并发与确定性

读走 `RWMutex` 读锁，写与清理走写锁；清理的每一步对读者原子，读者永远
读到最新值。逻辑时钟按追加顺序递增、槽位按最小空闲号分配、分数用精确
有理数比较且并列取最小段号，因此同一操作序列产生相同的清理顺序与段布
局（`Store.Dump` 可复现）。

### 本地验证

```bash
# 全量测试（含竞态检测），-v 可看到每个用例的输入、输出与判定依据
go test -race -v ./logstore

# 覆盖的场景：
#   TestCostBenefitTie        代价收益精确并列，取段号最小
#   TestTombstoneRetention    墓碑在旧块回收前搬迁保留、回收后死亡丢弃
#   TestMigrationPreservesAge 搬迁保留原写入时刻，年龄不重置
#   TestSpaceExhausted        无法净增空闲段时整体拒绝且不追加块
#   TestZeroLiveReclaim       存活率为零的段直接回收
#   TestConcurrentReadWriteClean 清理与读写并发，读者不回退
#   TestDeterministicLayout   同一操作序列布局逐字节一致
#   TestAccountingInvariantFuzz 随机序列每次操作后校验账目不变式
```
