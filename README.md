# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## sparsebitset：自适应容器的稀疏位集

`sparsebitset` 包实现元素为 `uint32` 的稀疏位集，按高 16 位分容器，
容器在两种表示间按基数自动转换。

### 容器转换阈值

- 元素高 16 位为容器键，低 16 位为容器内值。
- 基数 **不超过 4096** 的容器使用**有序数组**；基数 **大于 4096** 的
  容器使用 **1024 个 `uint64` 的位图**；基数恰为 **4096 必为数组**。
- `Add` 使数组容器基数达到 4097 时转为位图；`Remove` 使位图容器基数
  降到 4096 时转回数组；基数为 0 的容器立即从集合中移除。

### 规范化规则

- 容器表示类型是元素集合的**纯函数**：同一元素集合无论以何种顺序、
  何种增删历史得到，其容器表示与 `Stats` 都相同；相同操作序列重放
  结果完全一致。
- `And` 返回新集合，每个容器按结果基数重新规范化（<= 4096 为数组，
  > 4096 为位图），交为空的容器不出现，输入集合不被修改。
- `AddRange(lo, hi)` 加入半开区间 `[lo, hi)`（`hi` 为 `uint64`，可取到
  2^32）。`lo > hi` 报 `ErrLoGreaterThanHi`（优先于其他校验），
  `hi > 2^32` 报 `ErrHiExceedsMax`，`lo == hi` 为合法空操作；
  被拒绝的操作不改变集合。
- `Select(k)` 返回第 `k` 小（从 0 起）的元素，`k` 不小于基数时报
  `ErrSelectOutOfRange`；三种错误可用 `errors.Is` 区分。
- 所有方法可并发调用，效果等价于某个串行顺序（内部使用读写锁，
  `And` 按指针地址确定全局锁顺序以避免死锁）。

### 本地验证

```bash
# 单元测试与 2000 组随机操作对拍（-v 可查看每步输入/输出/判定依据）
go test ./sparsebitset/
go test -v -run TestFuzzAgainstNaive ./sparsebitset/

# 竞态检测
go test -race ./sparsebitset/

# 覆盖率
go test -coverprofile=coverage.out ./sparsebitset/
go tool cover -func=coverage.out
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
