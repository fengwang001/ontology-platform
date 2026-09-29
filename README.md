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

## 物化视图副本对账（`reconcile` 包）

`reconcile` 包基于区间哈希树，对两个物化视图副本做自上而下的对账：
只在组合哈希不一致的区间继续下钻，叶子层不一致即定位为差异键，
从而在不逐键扫描的前提下做到差异键不重不漏。

### 区间划分

- 键空间为 `[0, keyMax]`（左闭右闭的键集合），按 `fanout`（≥2）递归等分，
  每个节点对应一个左闭右开区间 `[lo, hi)`，叶子层一个节点对应一个键位。
- 当键空间大小不是 `fanout` 的整数次幂时，树宽向上取整到最近的
  `fanout^k`；超出 `keyMax` 的填充位标记为常量 `nilDigest`，
  其区间退化为 `[keyMax+1, keyMax+1)`，对账时直接跳过，不参与比较。
- 每个节点同时记录自己的 `[lo, hi)` 端点并参与哈希，因此相同内容
  出现在不同位置不会产生相同摘要。

### 哈希组合

- 叶子摘要 `H("leaf:v1" || key || present || value?)`：
  - 键不存在：`present=0`，不带值；
  - 键存在且值为零：`present=1, value=0`；
  - 键存在且值非零：`present=1, value=非零值`。
  三种情况摘要互不相同，因此“空区间 / 键不存在 / 值为 0”可以区分。
- 内部节点摘要 `H("node:v1" || lo || hi || child_0 || … || child_{fanout-1})`，
  固定拼接 `fanout` 个子摘要（不足的位置补 `nilDigest`）。
- `Put` / `Delete` 后只重算受影响叶子到根路径上的节点（增量维护），
  最终根哈希与写入顺序无关。

### 下钻规则

1. 对账输入是 `Replica.Snapshot()` 产生的不可变快照，哈希树与键值映射
   来自同一时刻，读路径不加锁、可并发，字段逐字段一致。
2. 两个快照必须同形：`fanout`、`keyMax`（及树深）一致，否则返回
   `ErrShapeMismatch`，不产生任何修改。
3. 从根开始深度优先、子节点从左到右比较组合哈希：
   - 哈希相等：整段内容一致，跳过整棵子树（判定依据记入比较序列）；
   - 哈希不一致且为内部节点：仅把其子节点加入下钻队列；
   - 哈希不一致且为叶子：该键记入 `DiffKeys`。
4. 结果包含 `DiffKeys`（升序、每个差异键恰好一次）与 `Comparisons`
   （完整的区间比较序列及判定依据）。同两个快照反复对账结果完全相同。

### 非法输入与失败原子性

| 错误 | 触发条件 |
| --- | --- |
| `ErrInvalidFanout` | `fanout < 2` |
| `ErrInvalidKeyMax` | `keyMax < 1` |
| `ErrInvalidMaxKeys` | `maxKeys < 1` 或 `maxKeys > keyMax+1` |
| `ErrKeyOutOfRange` | `Put/Get/Delete` 的键大于 `keyMax` |
| `ErrTooManyKeys` | 新增键会使键数超过 `maxKeys`（覆盖已有键不算新增） |
| `ErrShapeMismatch` | 对账双方键空间或扇出不同、快照为 nil |

所有校验先于变更执行：被拒绝的 `Put`/`Delete`/`Reconcile` 不会改变
任何键值或区间哈希；删除不存在的键是空操作。

### 日志

对账使用 `log/slog` 结构化日志（默认丢弃，可用
`reconcile/internal/logger.Set` 重定向），打印：输入（`fanout`、
`key_max`、双方键数）、每个差异键（双方是否存在与值）、以及每个区间的
判定依据（`combined hashes equal/differ`、`leaf hashes differ`）。

### 使用示例

```go
a, _ := reconcile.NewReplica(4, 1_000_000, 100_000)
b, _ := reconcile.NewReplica(4, 1_000_000, 100_000)
_ = a.Put(42, 7)

report, err := reconcile.Reconcile(a.Snapshot(), b.Snapshot())
// report.DiffKeys    => [42]
// report.Comparisons => 根到叶子的比较序列（含命中即剪枝的区间）
```

### 本地验证

```bash
# 全量测试 + 竞态检测
go test -race ./...

# 覆盖率
go test -coverprofile=coverage.out ./reconcile/
go tool cover -html=coverage.out

# 格式化与静态检查
gofmt -l .
go vet ./...

# 若默认 GOCACHE 目录只读，可指定临时缓存：
GOCACHE=/tmp/go-cache go test -race ./reconcile/
```

测试覆盖：键恰好落在区间边界（含 0、等分点、`keyMax`）、存在键与
零值键/不存在键的区分、200 组随机数据下与逐键暴力比较一致（含不重不漏
与根-叶比较链校验）、反复对账的确定性、写入并发下的快照字段一致性，
以及全部非法输入和日志内容断言。
