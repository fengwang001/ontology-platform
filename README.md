# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 自适应稀疏位集（`ontology` 包）

`SparseBitset` 存储 `uint32` 元素，以高 16 位为容器键、低 16 位为容器内值。

### 容器表示与转换阈值

- **数组容器**：有序去重的 `[]uint16`，基数 `<= 4096`；基数恰为 `4096` 时**必须**仍是数组。
- **位图容器**：`1024` 个 `uint64`（共 65536 位），基数 `> 4096` 时使用。
- `Add` 使数组基数到达 `4097` 时，就地转换为位图；`Remove` 使位图基数降到
  `4096` 时转换回数组；基数降到 `0` 时整个容器被移除。
- 表示类型只由当前元素集合决定：相同元素集合无论增删历史如何，容器类型与
  `Stats()` 完全相同。

### 规范化规则

- 每次修改后容器都处于规范表示（数组 `<= 4096`，位图 `> 4096`）。
- `And` 返回全新集合，每个结果容器按交集基数重新规范化，交为空的容器键不出现。
- `AddRange(lo, hi)` 加入半开区间 `[lo, hi)` 内所有 `uint32`，`hi` 可取到 `2^32`；
  `lo == hi` 是合法空操作。
- 拒绝（且不改变集合）：
  - `lo > hi` 返回 `ErrInvalidRange`（与 `hi > 2^32` 同时成立时先报此项）；
  - `hi > 2^32` 返回 `ErrRangeOutOfBounds`；
  - `Select(k)` 的 `k >= 基数` 返回 `ErrSelectOutOfBounds`。
- `Remove` 删除不存在的元素是合法空操作。
- `Rank(x)` 统计 `<= x` 的元素数（按有序容器键累加），`Select(k)` 返回第 k 小
  （从 0 起），两者满足 `Rank(Select(k)) = k+1`。
- 所有方法内部加锁，可并发调用，结果等价于某个串行顺序。

### 本地验证

```bash
# 全量测试（含 4095/4096/4097 转换、跨容器 AddRange、交集转数组、
# Rank/Select 互逆、2000 组随机对拍、重放与并发）
go test ./...

# 查看输入/输出/判定依据日志
go test -v ./ontology/

# 竞态检测
go test -race ./ontology/

# 格式与静态检查
gofmt -l .
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
