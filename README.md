# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 快照隔离提交管理器

`SnapshotCommitManager` 使用 `B` 个固定容量为 `A` 的哈希桶保存 `(key, lastCommitTimestamp)`。`B` 必须在 `[1,1024]`，`A` 必须在 `[1,16]`；越界时返回 `ErrInvalidConfig`，不创建管理器。

### 时间戳与事务

- `Begin()` 将共享计数 `n` 从当前值加一，以开始时间戳 `s` 标识事务并加入活跃集合。
- 非空写集成功提交时使用同一个计数的下一个值 `c=n+1`；开始时间戳和提交时间戳来自同一条严格递增序列，互不重复。
- 空写集 `Commit(s,nil)` 直接成功并返回 `0`，不分配提交时间戳、不查表、不受水位影响，只让 `s` 离开活跃集合。
- `Abort(s)` 只让 `s` 离开活跃集合，不修改时间戳、哈希表或水位。

### 提交次序

非空 `Commit(s,keys)` 先对键去重并按键升序处理，随后严格按以下顺序执行：

1. 拒绝检查：`s` 必须活跃；原始 `keys` 长度不得超过 64；每个键必须在 `[0,10^9]`。拒绝不改变任何状态，事务仍保持活跃。
2. 冲突检查：写集中某个键在其所属桶内已有表项，且表项提交时间戳大于 `s` 时，以 `CommitConflict` 中止。
3. 水位检查：淘汰水位 `Tm > s` 时，以 `CommitWatermark` 保守中止。
4. 提交：分配 `c=n+1`，先让 `s` 离开活跃集合，再以剩余活跃事务号最小值 `m` 作为清理边界；没有剩余活跃事务时 `m=+∞`。
5. 登记：已有同键表项只把时间戳更新为 `c`，不占用新桶位。新键所在桶满时，先删除桶内时间戳严格小于 `m` 的表项（这些表项不可能再与任何剩余事务冲突，且不抬高水位）；仍满时淘汰时间戳最小的表项，时间戳并列时选择较小键，并用被淘汰时间戳执行 `Tm=max(Tm,timestamp)`。

冲突或水位中止会让事务离开活跃集合，但不改变 `n`、表内容或 `Tm`。水位只由真实淘汰产生且单调不减；`Tm>s` 表示早于该水位开始的事务可能引用了已经被淘汰的历史表项，因此即使当前表中没有冲突，也会被安全地误报中止。

所有公共方法由同一个互斥区保护，并发调用的结果等价于某种串行交错。测试使用全历史提交记录验证成功提交之间不存在落在 `(s,c)` 内的同键先提交；没有发生任何淘汰（`Tm` 恒为 0）时，结果与不限容量的精确先提交者胜模型一致。

`Snapshot()` 返回时间戳、水位、活跃事务和按键排序后的桶内容深拷贝，可用于精确复现状态。

## 环境要求

- Go 1.26+（`go version` 确认）
- 若 shell 中没有 Go，可使用 `/usr/local/go/bin/go`；若默认缓存目录只读，可设置 `GOCACHE=/tmp/ontology-go-cache`。

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

# 2000 组随机序列与朴素模型对照（-v 会打印输入、输出和判定依据）
go test -race -v -run TestRandomSequencesMatchNaiveSimulation ./...

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
