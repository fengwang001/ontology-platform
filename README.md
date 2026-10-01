# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## delta 包：带确认的增量状态（δ-CRDT）复制

`delta` 包实现“键 → 无符号整数”的最大值映射副本，合并语义为逐键取较大值（缺省 0）。

### 核心定义

- **状态 S**：键到 uint64 的映射，合并即逐键 `max`。
- **增量序号 c**：本地下一增量序号，初始 0；只有膨胀的 `Apply` 才占用一个序号。
- **增量缓冲 D**：序号 → 增量（单键映射）的映射，容量上限为 `Cap`。
- **确认序号 ack[p]**：对端 p 已确认接收到的序号（单调不减，初始 0）。
- **已接收序号 seen[p]**：本副本从对端 p 因果连续接收到的序号（初始 0）。

### 操作规则

- `Apply(key, v)`：仅当 `v > S[key]`（缺省 0）时膨胀——置 `S[key]=v`、`D[c]={key:v}`、`c++`，返回 `true`；否则不做事、不占序号，返回 `false`（即使缓冲已满也不报错）。膨胀时若 `|D| == Cap`，整体拒绝（`ErrBufferFull`），状态不变。
- `DeltaTo(p)`：返回区间 `[a, b)`，其中 `a = ack[p]`、`b = c`；`a == b` 时返回空。否则增量组为 `D[a]` 至 `D[b-1]` 的逐键最大值合并，返回的映射是全新拷贝，不与内部状态别名。
- `Receive(p, a, b, group)`：`b <= seen[p]` 为陈旧重复——不合并、不改 `seen`，直接返回当前 `seen[p]`；`a > seen[p]` 为因果缺口——拒绝（`ErrGap`）；否则（`a <= seen[p] < b`）把 `group` 逐键合并进 `S` 并令 `seen[p] = b`。合并进 `S` 不产生 `D` 条目、不再转发。
- `Ack(p, n)`：`n > c` 拒绝（`ErrAckBeyondCounter`）；否则 `ack[p] = max(ack[p], n)`（较小的确认被忽略且不是错误）。随后回收：删除 `D` 中序号小于全部对端 `ack` 最小值的条目。回收后恒有 `|D| == c - min(ack)`，即序号恰等于 `min(ack)` 的条目仍保留。

### 错误与优先级

所有失败原因可区分（sentinel error）：构造期的 `ErrInvalidCap` / `ErrEmptyReplicaID` / `ErrEmptyPeerSet` / `ErrEmptyPeerID` / `ErrDuplicatePeer` / `ErrPeerIsSelf`；运行期的 `ErrBufferFull` / `ErrUnknownPeer` / `ErrInvalidInterval` / `ErrGap` / `ErrAckBeyondCounter`。`Receive` 按 **对端未知 → 区间非法（a ≥ b）→ 缺口** 的顺序只报第一个错误。任何被拒绝的操作都不改变状态。所有操作可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序。

### 本地验证

```bash
# 单元边界 + 三副本随机丢包/重复/乱序收敛（与朴素全状态合并对照）
go test -v ./delta

# 并发线性化冒烟（竞态检测）
go test -race ./delta
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
