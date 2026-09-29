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

## 单字段二级索引（`ontology` 包）

`ontology.New()` 返回一个单字段二级索引的增量维护器，按主键保存带一个整数字段的记录，并在该字段上维护有序索引。

- 写入：`Put(key, val)` / `Remove(key)`，或 `Apply([]Op{{Kind: OpUpsert|OpDelete, Key, Val}})` 原子批量写入。
- 查询：`Equal(val)` 返回该字段值的主键列表；`Range(lo, hi)` 返回区间内主键列表。
- 快照核对：`Snapshot()` 返回记录表与索引的深拷贝。

### 索引序

- 索引项是 `(字段值, 主键)` 二元组，逻辑顺序为字段值升序、同值内主键升序（字符串字典序）。
- 物理上按字段值分桶（`map[int][]string`），每个桶内主键始终保持升序；`Range` 先对命中的字段值排序再拼接各桶。
- 等值与范围查询返回的切片均为新分配拷贝，调用方修改不会影响内部状态。

### 范围边界

- `Range(lo, hi)` 为**左闭右开**：满足 `lo <= val < hi` 的记录被返回。
- `lo >= hi` 时返回空列表（不是错误）。

### 原子更新规则

- 更新已存在主键时“先从旧值索引组删除、再插入新值索引组”，两步在同一把写锁内完成，读线程要么看到旧组、要么看到新组，绝不会看到主键同时存在于两个组或两个组都不存在。
- 新值与旧值相等时为无操作，不会产生重复索引项；插入路径也有同组幂等保护。
- 删除会同时移除记录与对应索引项；值桶变空即回收。
- 批量 `Apply` 先整体预演校验，全部合法后才落盘，任一条非法则整批拒绝、记录表与索引均不变。可区分的原因：
  - `ErrEmptyKey`：主键为空字符串；
  - `ErrDeleteMissing`：删除当前不存在的主键；
  - `ErrUnknownOp`：批次中出现未知操作类型。
- 并发通过 `sync.RWMutex` 保证：写操作与其他写/读互斥；同一实例上的多个并发读者结果彼此一致（同一已提交状态下逐元素相同，可复现）。

### 本地验证：整体重扫核对

不信任增量索引时，可用 `Snapshot()` 做整体重扫（rebuild-and-compare）：遍历记录表按字段值重新分组排序，再与索引快照逐桶、逐元素比较，并用一次全范围 `Range` 核对拼接结果：

```bash
go test -race -v ./ontology/ -run TestRescanCrossCheck
```

其余场景（索引键更新、相等值无重复、范围左闭右开、非法批整体拒绝、并发读者一致性）见 `ontology/index_test.go`，测试日志会打印每步操作、各索引组内容、查询结果与判定依据：

```bash
go test -race -v ./ontology/
```
