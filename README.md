# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 变更流外键约束维护器（`ontology` 包）

`ontology.Maintainer` 在变更流上维护父表与子表，保证子表引用的父行始终存在。
内部用一张 `sync.RWMutex` 串行化写操作并允许并发只读查询；`Snapshot` 返回按主键排序的深拷贝，
并发读取同一实例得到的视图逐字段相同。

### 四类操作的判定顺序

所有判定都在同一把写锁内完成，失败即原子返回，父表、子表、引用计数与返回视图均不改变：

1. `insert_parent`：父行已存在时幂等返回成功（重放安全）；否则写入父表。
2. `delete_parent`：
   - 先判父行是否存在 —— 不存在返回 `parent_not_found`；
   - 再判引用计数是否为 0 —— 仍被引用返回 `parent_referenced`；
   - 否则删除父行。
3. `insert_child`：
   - 先判父行是否当前存在 —— 不存在返回 `child_parent_missing`；
   - 父行存在时，子行已存在则幂等成功，否则写入子表并将该父行引用计数加 1。
4. `delete_child`：
   - 先判子行是否存在 —— 不存在返回 `child_not_found`；
   - 否则删除子行并将对应父行的引用计数减 1。

四类错误（`child_parent_missing` / `parent_referenced` / `parent_not_found` /
`child_not_found`）互不重叠，可直接用于上游分流。

### 被拒子行不记忆，由上游重投

`insert_child` 因父行缺失被拒时，子行 ID 不会写入子表，引用计数也不变化——维护器对该子行
完全没有记忆。父行随后到达（父晚到）后，必须由上游重新投递同一条 `insert_child`；
重投在父行存在时成功，重复重投同一子行按幂等处理且不重复计数。因此只有成功操作才允许进入
提交日志，被拒操作不记录、靠重投恢复。

### 查询、自检与不变量

- `Snapshot()`：并发安全，返回排序后的父表/子表深拷贝。
- `RefCount(parentID)`：返回当前引用计数；父行不存在时为 `(0, false)`。
- `CheckInvariants()`：任一时刻都应通过——不存在指向缺失父行的子行，
  且每个父行的引用计数恰等于子表中实际引用它的子行数。

### 本地验证：重放成功操作核对结果

把所有成功的 `Apply` 操作按顺序收集成日志（被拒操作不收集），之后用 `ontology.Replay`
在全新维护器上重放，并与当时的 `Snapshot()` 逐字段比较：

```go
var committed []ontology.Op
v, err := m.Apply(op)
if err == nil {
    committed = append(committed, op) // 只有成功操作入日志
}
want := m.Snapshot()
res := ontology.Replay(committed, want) // res.OK 必须为 true
```

若日志中误入被拒操作（如父行缺失的 `insert_child`），重放会在该操作处以同样的
`child_parent_missing` 失败；重放成功但视图不同则返回 `replay_mismatch`。

对应单测：`ontology/maintainer_test.go` 覆盖父晚到、删被引用父行、删不存在的父/子行、
幂等插入与引用计数增减、成功日志重放、并发读写自检等场景；
`go test -v` 输出中逐条打印“操作 / 成功或错误类型 / 视图 / 判定依据”。

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
