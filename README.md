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

## 点版本向量兄弟集合存储（dvvstore）

`dvvstore` 包实现了基于点版本向量（dot version vector）的每键兄弟集合存储，用于保存并发写产生的多个值，并保证多副本任意顺序合并后逐字段一致。

### 核心概念

- **点（dot）**：`(id, n)`，`id` 为节点标识（非空字符串），`n` 为该节点在该键下单调递增的计数。每次写产生一个全局唯一的新点。
- **seen（已发点向量）**：每个键保存 `节点标识 -> 该节点在此键上已发出的最大计数`，缺省视为 `0`。`Get` 返回的“上下文”就是该键 seen 的拷贝。
- **ctx（写入上下文）**：写者已知的版本向量（`节点标识 -> 计数`，缺省 0），用于表达“我已经见过哪些点”。
- **兄弟（sibling）**：并发存活的 `(点, 值)`；值是字符串，允许空串。`Get` 按“节点标识字典序、计数升序”返回。

### Put 规则

`Put(key, id, ctx, value)`：

1. 新点为 `(id, seen[id]+1)`——计数只由本侧 seen 决定，不是 `ctx[id]+1`，所以空上下文连续写 A 得到 `(A,1)`、`(A,2)`。
2. 删除所有满足 `ctx[j] >= m` 的兄弟点 `(j,m)`（恰等也算已覆盖；`ctx[j] == m-1` 则保留为并发兄弟）。
3. 加入新兄弟，并把 `seen[id]` 置为新计数。
4. 构造时 `Cap`（每键兄弟数上限）小于 1 返回 `ErrInvalidCap`；Merge 不受 Cap 限制。

Put 的错误按以下**固定优先级**只报第一个，且被拒绝的操作不改变任何兄弟与 seen（先在独立切片上计算剔除结果，超限才拒绝）：

1. 键为空：`ErrEmptyKey`
2. 节点标识为空：`ErrEmptyNodeID`
3. ctx 含空节点标识：`ErrEmptyContextNode`
4. ctx 中 `ctx[j] > seen[j]`（上下文超前，包括不认识的节点给出正数）：`ErrContextAhead`
5. 剔除后再加入新兄弟会使兄弟数超过 Cap：`ErrCapExceeded`（Cap 在剔除**之后**判定，所以 `Cap=1` 时全上下文写可成功、空上下文并发写被拒）

### Get 规则

键不存在返回空（非 nil）兄弟列表与空上下文，不是错误；返回的切片与 map 均为深拷贝，调用方修改不会影响内部状态。

### Merge 规则

`Merge(other)` 逐键并入另一存储（键集取两侧并集）：

- 本侧兄弟 `s` 保留当且仅当：对侧该键有同点兄弟，**或** 对侧该键 `seen[s.id] < s.n`（对侧没见过这个点）；对侧兄弟按本侧对称判定。
- 保留者按点去重取并集；同点而值不同时取**字典序较大**的值。
- 两侧 seen 逐节点取较大值；只在一侧存在的键整体深拷贝（可超过本侧 Cap）。
- Merge 先对 `other` 做快照再持自身锁修改，因此两个存储互相并发 Merge 不会死锁，`Merge` 自身是空操作。

所有方法用互斥锁串行化，并发调用等价于某个串行顺序。同批 Put 分散到多副本后，以任意顺序、任意次数互相 Merge，结果满足交换、结合、幂等，状态逐字段相同。

### 本地验证

```bash
# 常规测试（含与朴素模拟逐操作对照的三副本随机调度测试）
go test ./dvvstore -v

# 竞态检测（并发 Put / 双向并发 Merge / 拒绝不改状态）
go test -race ./dvvstore

# 全量验证
go test -race ./...
gofmt -l .
go vet ./...
```
