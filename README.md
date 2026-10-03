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

## maglev 包：带权重与限速迁移的一致性哈希查找表

`maglev` 实现 Maglev 式一致性哈希查找表，构造参数为表大小 `M`（2 到
65537 之间的素数）与每次迁移上限 `Lim`（1 到 M），配置非法时整体拒绝
（`ErrInvalidConfig`）。

### 目标表 T* 的构造

- 后端按名字字节序升序排成 `b0 .. b(n-1)`，后端 `i` 的排列为
  `perm_i(j) = (offset_i + j * skip_i) mod M`（`j` 从 0 到 M−1），并各自
  维护指针 `j_i`（初值 0，跨轮保持，不回到 0）。
- 表初始全空，循环直到填满：每轮按 `b0 .. b(n-1)` 顺序，每个后端在自己
  的一轮里依次占据 `weight_i` 个槽；每次从 `perm_i(j_i)` 起向后越过已占
  槽找到第一个空槽占据，并使 `j_i` 加一。
- 表一旦填满立即停止：本轮其余占据与后面的后端都不再填。`T*` 只取决于
  后端集合，与登记、移除顺序无关。

### 迁移规则

当前表 `cur`（初为全空）不直接等于 `T*`，而是分批向 `T*` 迁移：

- **必改槽**：`cur[s]` 为空，或其所属后端已不在后端集合中。
- **可选槽**：其余 `cur[s] != T*[s]` 的槽。
- 每次 `AddBackend`/`RemoveBackend` 成功后，先把全部必改槽改成 `T*` 的
  归属（不受 `Lim` 限制），再按槽位号升序最多改 `Lim` 个可选槽。
- `Step()` 不改后端集合，只按槽位号升序最多改 `Lim` 个可选槽；后端集合
  为空时什么都不做。
- 三者返回本次实际改变归属的槽位数；`Pending()` 返回 `cur` 与 `T*` 不同
  的槽位数。`Step` 恰使 `Pending` 减少 `min(Lim, Pending)`，反复 `Step`
  至多 `⌈M/Lim⌉` 次后必为 0。

### 查询与错误

- `Lookup(h)` 返回 `cur[h mod M]` 的后端名；后端集合为空时报
  `ErrNoBackends`。`Table()` 返回 `cur` 的拷贝。
- `AddBackend` 的拒绝原因按顺序只报第一个：`ErrEmptyName`（名为空）→
  `ErrOutOfRange`（offset/skip/weight 越界）→ `ErrNameExists`（重名）→
  `ErrTooManyBackends`（后端数已达 M）。`RemoveBackend` 对不存在的名字报
  `ErrBackendNotFound`。被拒绝的操作不改变 `cur` 与后端集合。
- 所有方法可并发调用，结果等价于某个串行顺序；相同操作序列重放得到完全
  相同的返回值与表。

### 本地验证

```bash
# 全部单测（含 2000 组随机后端集合/操作序列与朴素逐步构造对照）
go test ./maglev

# 查看随机对照日志（输入、输出与判定依据）
go test ./maglev -run TestRandomAgainstNaive -v

# 竞态检测
go test -race ./maglev
```
