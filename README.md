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

## siblings：点版本向量兄弟集合存储

`siblings` 包实现了一个可按多副本并发写、可合并的键值存储。

### 核心概念

- **点 (dot)**：一次写入的身份，记为 `(id, n)`。`id` 是节点标识，`n` 是该节点在**某个键下**的单调计数。
- **兄弟 (sibling)**：并发写产生的并存值。每个键保存一个兄弟列表，每个兄弟是一个点加上对应的值（值可为空串）。
- **seen（已发点向量）**：每个键一份，记录该键下每个节点已发出的最大计数（缺省为 0）。新点的计数取自 `seen[id]+1`，与调用方传入的上下文无关，因此空上下文重复写也会得到 `(A,2)` 而不是重复的 `(A,1)`。
- **上下文 ctx**：节点标识到计数的映射（缺省为 0），表示本次写"看到"了哪些历史写。`Get` 返回的上下文就是该键 `seen` 的拷贝，可直接用于下一次 `Put`。

### Put 的剔除规则与错误优先级

`Put(key, id, ctx, value)` 先做全部校验、再做任何修改，被拒绝的写不改变任何键的兄弟与 `seen`。校验按以下顺序只报告第一个错误（均为可用 `errors.Is` 区分的哨兵错误）：

1. `ErrEmptyKey`：键为空；
2. `ErrEmptyID`：节点标识为空；
3. `ErrEmptyCtxNode`：ctx 含空节点标识；
4. `ErrCtxAhead`：ctx 中某节点计数大于该键 `seen` 中的对应值（缺省 0）；
5. `ErrCapExceeded`：剔除被覆盖兄弟后再加入新兄弟会超过 `Cap`。

通过校验后：新点为 `(id, seen[id]+1)`；删除所有满足 `ctx[j] >= m` 的兄弟点 `(j, m)`（**恰等于也算被覆盖**，小 1 则保留）；然后加入新兄弟并把 `seen[id]` 置为新计数。`Cap` 在**剔除之后**判定，因此 `Cap=1` 时完整上下文的覆盖写成功、空上下文的并发写被拒。构造时 `Cap < 1` 返回 `ErrInvalidCap`。

### Merge 的合并规则

`Merge(other)` 先对 `other` 取快照再修改自身，逐键并入：

- 本侧兄弟 `s` 保留当且仅当：对侧该键有同点兄弟，或对侧该键 `seen[s.id] < s.n`（即对侧还没见过这个点）；对侧兄弟按本侧对称判定；
- 保留者按点去重取并，同点不同值时取字典序较大的值；
- 两侧 `seen` 逐节点取较大值；
- 只在一侧存在的键整体深拷贝；
- `Merge` 不受 `Cap` 限制。

合并满足交换、结合、幂等：同一批 `Put` 分散到多个副本后，以任意顺序、任意次数互相 `Merge`，所有副本收敛到逐字段相同的状态。

### 并发语义

所有方法可并发调用，效果等价于某个串行顺序。`Merge` 的快照-再-加锁策略保证两个存储互相并发 `Merge` 不会死锁；对自身 `Merge` 是合法空操作。`Get`/`Keys` 返回的切片与映射均为拷贝，不与内部状态别名。

### 本地验证

```bash
# 全部测试（含朴素模拟对照、随机三副本交换/结合/幂等、并发压测）
go test ./siblings/

# 竞态检测 + 打印每个用例的输入、输出与判定依据
go test -race -v ./siblings/
```
