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

## 带确认的增量状态 CRDT（`ontology` 包）

`ontology/delta_crdt.go` 实现了一个 **δ-CRDT 复制副本**：数据类型为
`键 -> uint64` 的最大值映射（Grow-Only Max Map），合并语义为逐键取较大值，
缺省键视为 `0`。所有方法由一把互斥锁保护，并发调用的效果等价于某个串行顺序。

### 副本状态

由 `New(id, peerIDs, Cap)` 构造，`id` 非空、`peerIDs` 非空、各项非空、互不相同
且不含 `id`、`Cap >= 1`，否则整体拒绝（见下文错误表）。内部维护：

- `S`：当前状态（逐键最大值映射）。
- `c`：下一增量序号，初始 `0`；每次膨胀占用一个序号并自增。
- `D`：序号 -> 单键增量的缓冲，`D[i] = {key: v}`。
- `ack[p]`：对端 `p` 已确认连续收到的序号上界，初始 `0`。
- `seen[p]`：本副本从 `p` 已连续接收到的序号上界，初始 `0`。

### 操作语义

- `Apply(key, v)`：仅当 `v > S[key]`（缺省 `0`）才是**膨胀**：
  置 `S[key]=v`、写 `D[c]={key:v}`、`c++`，返回 `(true, nil)`；
  否则什么都不做、不占序号，返回 `(false, nil)`（缓冲满时亦然）。
  膨胀时若 `|D| == Cap`，整体拒绝并返回 `ErrBufferFull`，状态不变。
- `DeltaTo(p)`：发送半开区间 `[a, b)`，其中 `a = ack[p]`、`b = c`。
  `a == b` 返回零值空消息；否则返回 `(a, b, group)`，`group` 是
  `D[a] … D[b-1]` 的逐键最大值合并。返回的 map 是拷贝，与内部状态无别名。
- `Receive(p, a, b, group)`：因果接收，返回当前 `seen[p]`。
  - `b <= seen[p]`：陈旧重复，不合并、不改 `seen`，返回 `seen[p]`。
  - `a > seen[p]`：有缺口（缺 `[seen[p], a)`），拒绝，返回 `(seen[p], ErrGap)`。
  - `a <= seen[p] < b`：把 `group` 逐键最大合并进 `S`，置 `seen[p] = b`。
  - 合并进 `S` **不产生 D 条目、不转发**，也不受本地 `Cap` 限制。
- `Ack(p, n)`：`n > c` 返回 `ErrAckBeyondCursor`（拒绝且不做回收）；
  否则 `ack[p] = max(ack[p], n)`——较小的回退确认被忽略且不是错误——
  随后计算 `m = min(ack[*])`，删除所有序号 `< m` 的缓冲条目。

### 关键不变量

- 回收后恒有 `|D| == c - min(ack)`；序号**恰等于**最小 ack 的条目保留。
- 区间以确认进度为界：重发得到的是同序号区间的幂等重放；
  逐键最大合并保证重复、重叠、乱序交付结果可精确复现。
- 只要丢失的增量按对端 `seen` 重发直到全部送达，任意交付顺序下各副本
  `S` 逐键相同（最终一致性）。

### 错误优先级（只报第一个）

- 构造：`Cap < 1` → `ErrInvalidCap`；自身 id 空 → `ErrEmptyID`；
  对端集合空 → `ErrNoPeers`；对端 id 空 → `ErrEmptyID`；
  含自身 → `ErrSelfInPeers`；重复 → `ErrDuplicatePeer`。
- `DeltaTo` / `Ack`：未知对端 → `ErrUnknownPeer`；
  `Ack` 的 `n > c` → `ErrAckBeyondCursor`。
- `Apply` 膨胀且 `|D| == Cap` → `ErrBufferFull`；非膨胀永不报错。
- `Receive`：**未知对端 → 区间非法（`a >= b`，`ErrInvalidInterval`）→
  缺口（`ErrGap`）**，按此顺序只报第一个。
- 任何被拒绝的操作都不改变状态（原子性）。

### 本地验证

```bash
# 全量测试（含 -v 可看到每步输入、输出与中文判定依据）
go test -race -v ./...

# 只跑三副本随机丢包/重复/乱序收敛对照
go test -run TestThreeReplicaChaosConvergence -v ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

测试覆盖：`a == seen` 可接上与 `a == seen+1` 为缺口、`b == seen` 为陈旧、
非膨胀（`v` 等于现值）不占序号、缓冲满时非膨胀成功而膨胀被拒、
Ack 回退被忽略、`n == c` 合法而 `n == c+1` 被拒、序号恰等于最小 ack 的条目保留、
三副本在多个随机种子下经丢包/重复/乱序后收敛，并与**每步做全状态合并的
朴素复制副本**逐键对照；并发压力测试在 `-race` 下校验串行等价与无别名。
