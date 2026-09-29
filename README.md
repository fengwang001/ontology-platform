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

## 基于区间哈希的物化视图副本对账（`reconcile` 包）

`reconcile` 包把两个副本之间的逐键对账变成一棵摘要树上的哈希下钻：只在哈希不一致的区间递归等分，叶子不一致才记为差异键，因此差异键精确、不重不漏。

### 区间划分

- 键空间固定为整数区间 `[0, size)`，所有区间左闭右开，按 `fanout`（>= 2）递归等分。
- 树建立在规则网格上：根层级 `depth = ceil(log_fanout(size))`，网格跨度 `fanout^depth`；`[size, fanout^depth)` 内的槽位是永久的“结构空位”，与真实空区间使用不同域标签，因此即使根不满扇出网格，结果依然确定且可复现。
- 每个层级的节点宽度恒为 `fanout^level`，按网格对齐；例如 `size=8, fanout=2` 时边界为 `0,2,4,6,8`，跨边界的键被独立路由。
- 树的递归深度有界（叶子宽度为 1），对账一定终止。

### 哈希组合

- 叶子摘要使用 SHA-256，并通过首字节域标签区分三种状态：
  - 结构空位叶子：`H(0x04)`，对应越界槽位；
  - 空叶子（键不存在）：`H(0x01)`；
  - 存在键：`H(0x02 || len(value)（大端 uint64）|| value)`。
- 因此“键不存在”“值为零的键（非 nil 的零长度值）”“越界槽位”三者摘要互不相同；零值键不会被当作空键。
- 内部节点摘要为 `H(0x03 || level || lo || hi || childCount || childDigest_0 || …)`，固定宽度字段消除拼接歧义，`level/lo/hi` 使摘要绑定其网格位置。
- 空内部节点的摘要在构造副本时按层级预算一次（普通空区间、结构空区间、跨越 `size` 的截断区间各一张小表），无需存储整棵空树。

### 写入与增量维护

- `Put(key, value)`：拒绝越界键与 `nil` 值；非 nil 零长度值合法，表示“值为零的键”。
- `Delete(key)`：删除键；删除不存在的键是空操作。
- 每次写入/删除只重算该键叶子到根路径上的 `depth + 1` 个节点；区间变空时删除缓存节点，摘要回到预算的空基线。
- 非法输入（参数非法、键越界、初始数据条数超过 `size`）在校验通过前不触碰任何状态。

### 下钻对账

1. 校验两个副本非 nil 且 `size`、`fanout` 完全一致，否则分别返回 `ErrNilReplica` / `ErrShapeMismatch`，对账不改变任何副本。
2. 对两个副本各取一次读快照（`sync.RWMutex`，同一副本自对账时不重复加锁），保证逐字段一致；写入只取写锁，对账期间并发安全。
3. 从根开始比较组合哈希：
   - 相等：整个区间内容一致，跳过（不下钻）；
   - 不相等且为内部节点：记录比较后按升序访问全部 `fanout` 个子区间；
   - 不相等且为叶子：记录差异键，附两侧值与存在性（缺失 / 零值 / 不同值可区分）。
4. 比较按前序、子区间按键升序产生，完全确定：同一对副本反复对账得到完全相同的差异键集合与比较序列。

### 日志

`DefaultComparer` 使用 `log/slog` 文本格式输出到 stderr，记录：对账输入（两侧名称、`size`、`fanout`、键数）、每个区间的“相等跳过 / 不一致下钻”判定依据、每个叶子差异的存在性与值长度及原因、以及最终差异键摘要。也可以用 `&Comparer{Writer: w}` 或自定义 `Logger` 捕获日志。

### 本地验证

```bash
# 全量测试（随机 200 组数据与逐键暴力比较对拍）
go test ./reconcile -v

# 竞态检测（含并发读写对账）
go test -race ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# 静态检查与格式
go vet ./...
gofmt -l .
```

测试覆盖：键恰好落在区间边界与键空间边缘、存在键 vs 零值键 vs 缺失键的区分、增量哈希与重建哈希一致、随机数据下与逐键比较完全一致（含更新/插入/删除/零值）、非法参数与形状不匹配被拒绝且无副作用、重复对账序列确定、并发读写在 `-race` 下安全。
