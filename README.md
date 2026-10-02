# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## Undo 日志段管理器

`undo.Manager` 管理回滚段槽位 `S`、每页记录数 `K` 与总页预算 `PB`。每个活跃事务最多拥有插入 undo（I）和更新 undo（U）两个段；含 `n` 条记录的段占 `max(1, ceil(n/K))` 页和一个槽位。

- **释放时机**：`Commit(t)` 立即对 I 段执行 Release，U 段按提交序号 `trx_no` 追加到历史链尾部；`Rollback(t)` 立即 Release I/U 两个段。空事务提交也会消耗一个 `trx_no`，回滚不消耗。
- **缓存条件**：Release 时，仅当段当前只占 1 页且 `4n <= 3K` 才进入同类型缓存并保留槽位和页；否则归还槽位和全部页。例如 `K=4` 时 `n=3` 进缓存，`n=4` 整体释放，超过 1 页也整体释放。
- **复用次序**：新建 I/U 段前先弹同类型缓存的栈顶（LIFO），记录数清零并保留槽位和第 1 页，因此不新增槽位或页；缓存为空时先检查空闲槽位，再检查页预算。追加记录跨页时只额外申请 1 页。
- **读视图水位**：`OpenView()` 的 `limit` 固定为打开时已发 `trx_no` 数加一。清理限值 `PL` 是所有打开视图 `limit` 的最小值；没有视图时为当前已发 `trx_no` 数加一。
- **历史回收**：`Purge(n)` 从历史链头部开始，只回收满足 `trx_no < PL` 的段，最多回收 `n` 个并按顺序返回其 `trx_no`；遇到第一个不满足条件的段立即停止。回收时再次执行 Release，因此小的单页 U 段会进入 U 缓存。
- **并发与拒绝**：所有操作由同一互斥保护，可线性化。错误优先级为参数非法、事务不存在、事务已终止、无空闲槽位、页预算不足；被拒绝的申请不会留下段、槽位或页占用。

本地验证：

```bash
# 定点、并发与 2000 组朴素模型随机对照（-v 可查看每组输入、输出与判定依据）
go test -race -v ./undo

# 全量检查
go test ./...
go vet ./...
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
