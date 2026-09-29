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

## 单字段二级索引增量维护器（`secindex`）

`secindex` 包在按主键存放记录（记录仅含一个 `int64` 字段）的同时，增量维护该字段上的二级索引。

### 索引序

- 每条记录的索引项是二元组 `(字段值, 主键)`。
- 存放顺序：先按字段值升序；同一字段值的主键组内按主键升序（Go 的 `sort.SearchStrings` 定序）。
- `Lookup(v)` 返回字段值等于 `v` 的主键升序列表；`IndexGroups()` 按字段值升序返回各组深拷贝。

### 写入与原子更新规则

- `Upsert(key, value)`：主键不存在则插入；已存在时**先从旧值索引组删除，再插入新值索引组**，两步在同一把写锁内完成，读请求只能看到更新前或更新后的状态。
- 新值等于旧值时为**无操作**，不移动索引项、不产生重复项。
- `Delete(key)` 同时删除记录与索引项；主键组清空后字段值桶一并移除。
- `Apply([]Op)` 为批量原子写：先做整批预演校验（空主键、未知操作类型、删除不存在的主键、空批次），任一非法则**整批拒绝**，记录表与索引完全不变；校验通过后才在单次加锁内提交。拒绝原因可用 `errors.Is` 区分：`ErrEmptyKey`、`ErrKeyNotFound`、`ErrInvalidOpKind`、`ErrEmptyBatch`，错误中的 `BatchError.Index` 指明违规操作下标。
- 所有查询返回独立切片拷贝，调用方修改返回值不会污染索引。

### 范围边界

- `Range(lo, hi)` 为**左闭右开** `[lo, hi)`：包含字段值等于 `lo` 的记录，排除等于 `hi` 的记录。
- 跨值组结果按主键全局升序（先收集区间内全部主键再排序）。
- `lo >= hi`（空区间或反转区间）返回空的非 nil 切片。

### 并发语义

- `sync.RWMutex` 保护：单次查询整段持有读锁，写操作持写锁；`Snapshot()` 在一次读锁内原子返回 `Lookup(0)`、`Lookup(1)`、`Range(0,1)` 与全量索引组，需要多查询一致视图时使用它。
- 同一实例的多个读请求观察到同一个已提交状态时，结果逐元素相同；任一 `Upsert` 返回后，该主键只出现在新值索引组。
- 请使用 `go test -race ./secindex` 验证，测试包含屏障同状态多读与无协调读写压测两类场景。

### 本地验证：整体重扫核对

增量维护的正确性可随时用全量重扫交叉核对：`VerifyByRescan()` 忽略维护中的索引，扫描整个记录表重建期望索引，逐字段值组比较主键列表与条目总数，一致返回 `nil`，否则返回 `*RescanMismatch`（含首个分歧位置与双方结果）。测试在每次操作后、并发每轮与结束时都会执行该校验。

```bash
# 带日志运行（打印操作、各索引组、查询结果与判定依据）
go test -race -v ./secindex

# 覆盖率
go test -cover ./secindex
```
