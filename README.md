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

## 增量前缀和视图（`prefixsum` 包）

`prefixsum` 包在有序整数键上增量维护每个**存在键**的前缀和，
支持并发写入/删除与查询，保证查询结果与朴素全量重算逐键一致。

### 前缀和定义

- 键 `k` 的前缀和 = 当前所有存在且键值不大于 `k` 的键的值之和，即
  `P(k) = Σ value(x)，x 存在且 x ≤ k`。
- 前缀和只对存在键有定义；查询不存在的键（含从未写入或已删除）返回键不存在错误。
- 值与和均为 `int64`；任何中间或最终和值溢出 `int64` 时该次写入被整体拒绝。

### 操作与受影响键计数

- `Put(key, value)`：键不存在则插入，存在则改值。返回操作后前缀和数值发生变化的存在键个数：
  - 插入：新键一律计 1，再加上所有键严格更大的存在键（它们的前缀和平移新值）。
  - 改值：新旧值不同计时为该键与所有更大的存在键；新旧值相同计 0。
    键自身的前缀和是否变化以数值为准，差值为 0 时不计。
  - 删除：被删键本身不计，仅统计所有键严格更大的存在键。
- `Delete(key)`：删除存在键；键不存在返回键不存在错误。
- `PrefixSum(key)`：返回存在键的前缀和。
- `Snapshot()`：按键升序返回 `[]Entry{Key, Value, PrefixSum}` 的只读副本。
- `SelfCheck()`：对当前视图做朴素全量重算，逐键比对快照与单点查询。

### 边界与错误类别

视图由 `Config{MinKey, MaxKey, MaxKeys}` 限定闭区间与容量。
所有错误通过 `ErrorReason(err)` 返回互不相同、可区分的原因，
且任何被拒操作均在加锁状态下先校验、后函数式提交，失败不留痕：

- `invalid_argument`：nil 视图、`MinKey > MaxKey`、`MaxKeys <= 0`。
- `key_out_of_range`：键超出 `[MinKey, MaxKey]`。
- `key_not_found`：查询或删除当前不存在的键。
- `too_many_keys`：插入会使存在键数超过 `MaxKeys`（改已有键的值不新增键，不受此限）。
- `overflow`：写入会使某个前缀和超出 `int64`（即使总共和合法，中间前缀越界也拒绝）。

### 并发

内部使用 `sync.RWMutex`：多个执行体可并发调用
`PrefixSum`/`Snapshot`/`SelfCheck`/`Len`，并可与 `Put`/`Delete` 并发。
树更新采用函数式路径重建，提交前的失败不触碰旧状态；
改值再改回、插入后删除，视图逐键恢复原样。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 重复执行（含并发读写压测）
go test -race -count=2 ./prefixsum

# 查看每步输入、前缀和与判定依据的详细日志
go test -race -v ./prefixsum

go vet ./...
gofmt -l .
```

测试覆盖：插入、改值（含改回原值）、删除、零值键、负值键、
四类输入拒绝及拒绝后状态逐项不变、前缀和溢出（含中间前缀溢出）、
4000 步固定种子随机操作与朴素模型逐键对照，以及多执行体并发读写自检。
