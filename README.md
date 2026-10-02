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

## 两区缓冲池 LRU

`twosizelru.Manager` 用两条独立但相邻的链表管理容量内的页：

- 年轻区 `Y`：头是最近使用端，尾接老年区。
- 老年区 `O`：头是新读入页的位置，尾是淘汰端。
- 逻辑顺序为 `Y` 头到尾，再接 `O` 头到尾。
- 当前页数为 `len` 时，老年区目标大小是 `tgt=floor(len*ρ/100)`。

每次 Access 缺页插入后，或老年页满足停留条件晋升后，执行一次 Rebalance：

- 若 `|O| < tgt`，从 `Y` 尾取页移到 `O` 头，直到达到目标或 `Y` 为空。
- 若 `|O| > tgt+Tol`，从 `O` 头取页移到 `Y` 尾，直到 `|O| == tgt+Tol`。
- 池满淘汰后不单独 Rebalance；新页插入到 `O` 头后只 Rebalance 一次。

晋升与读入规则：

- Access 命中 `Y`：页移到 `Y` 头，不改变 `first`。
- Access 命中 `O` 且 `first` 为空：记录 `first=now`，页不移动。
- Access 命中 `O` 且 `now-first >= T`：晋升到 `Y` 头并 Rebalance；差值小于 `T` 时不移动。
- Access 缺页：新页插入 `O` 头并记录 `first=now`。
- Prefetch 缺页：同样插入 `O` 头，但 `first` 保持为空；Prefetch 已在池中的页只推进时钟。

池满缺页时从 `O` 尾向头寻找第一个 `pin==0` 的牺牲页；若老年区全部被钉住，再从 `Y` 尾向头寻找。两区都没有可淘汰页时拒绝读入。Pin/Unpin 只调整钉住计数，不推进时钟；淘汰时会清除该页的 `first` 与 `pin`。

拒绝原因使用以下优先级，只返回第一个：参数非法、时钟回退、页不在池中、Unpin 下溢、全部被钉。所有被拒绝的操作都不会修改链表、时钟、`first` 或 `pin`。管理器内部使用互斥锁，公开操作可并发调用。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测
go test -race ./twosizelru

# 查看 2000 组随机序列的输入、输出、判定依据和最终链表
go test -run TestRandomSequencesAgainstReference -v ./twosizelru
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
