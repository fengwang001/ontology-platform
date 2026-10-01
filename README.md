# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 区间树时钟副本注册表

`ontology` 包提供并发安全的命名副本注册表 `Registry`：

- `Seed(name)` 创建 `(1;0)`。
- `Fork(name, child)` 派生身份，父副本和子副本保留同一个事件树。
- `Event(name)` 先执行 `fill`；只有结果与原事件树不同才采用，否则执行 `grow` 并规范化。
- `Peek(name)` 返回只读事件快照 `(0;event)`，不暴露副本身份。
- `Join(a,b)` 在身份不重叠时原子合并，并删除 `b`。
- `Compare(a,b)` 只比较事件因果，返回 `Before`、`After`、`Equal` 或 `Concurrent`。

身份树只能是 `0`、`1` 或 `(l,r)`。规范化时 `(0,0)` 化为 `0`，`(1,1)` 化为 `1`。事件树只能是非负整数 `n` 或 `(n,l,r)`，其中：

- `min(e)`、`max(e)` 分别取整数自身，或节点首分量加两棵子树对应极值。
- `lift(m,e)` 给整数或节点首分量加 `m`。
- 节点规范化时若两个整数子树相等，则合并成整数；否则减去两个子树最小值中的较小者，保证规范化后的每个节点至少有一棵子树最小值为 `0`。
- `fill` 按身份补全当前事件树；`grow` 按最小代价路径新增事件，规则按题面顺序匹配，`cl==cr` 时选右子树；节点身份遇到整数事件会先展开成 `(n,0,0)`，并加 `1000000` 代价。

Join 的身份使用 `sum` 合并，非零身份结构不能与 `1` 或冲突分支合并，否则返回 `ErrIdentityOverlap`。事件使用提升后逐分支取上确界。Compare 由事件树的两个方向 `leq` 判定：仅正向为 `Before`，仅反向为 `After`，双向为 `Equal`，双向都不为 `Concurrent`。

所有方法都在互斥锁下校验并更新，拒绝操作不会留下部分状态；`Join` 删除名字后，再使用该名字会返回 `ErrUnknownReplica`。树节点本身不可变，快照不会与内部状态共享可写结构。

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

# 查看 2000 组随机 Fork/Event/Join 序列的输入、输出、朴素参考和判定依据
go test -run TestRandomSequencesAgainstNaiveReference -v ./...

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
