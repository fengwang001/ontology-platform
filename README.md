# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## prefixsum：有序键上的增量前缀和视图

`prefixsum` 包在有序键（int64）上增量维护前缀和视图，底层为确定性 treap
（优先级由键哈希生成，树形只取决于键集合，与操作顺序无关，结果可复现）。

### 前缀和定义

对任意**存在键** `k`，其前缀和为所有存在且不大于 `k` 的键的值之和。
前缀和只对存在键有定义；查询不存在的键返回 `ErrKeyNotFound`。

### 受影响键计数

每次 `Put` / `Delete` 返回操作后前缀和发生变化的键个数：

- 插入新键：新键一律计一；若插入值非零，其上方（键更大）所有存在键各计一。
- 改值：增量为零时计零；否则键不小于目标键的所有存在键各计一。
- 删除：被删键不计；若被删值非零，其上方所有存在键各计一。

### 边界与错误类别

所有非法输入整体拒绝、失败不留痕（视图状态不变），错误类别互不相同、
可用 `errors.Is` 区分：

| 错误 | 含义 |
| --- | --- |
| `ErrInvalidArgument` | 非法参数（nil 接收者、`minKey > maxKey`、`maxKeys <= 0`） |
| `ErrKeyOutOfRange` | 键越界，不在 `[minKey, maxKey]` 内 |
| `ErrKeyNotFound` | 键不存在（删除或查询时） |
| `ErrTooManyKeys` | 插入新键将使键数超过 `maxKeys`（改值既有键不受限） |
| `ErrOverflow` | 操作将使某存在键的前缀和超出 int64（和值不溢出） |

### 并发与自检

`PrefixSum` / `Entries` / `Check` / `Len` 使用读锁，可被多个执行体并发调用，
并与 `Put` / `Delete` 并发；`Check` 校验树结构不变量并将增量前缀和与朴素
重算逐键比对。写入改值再改回、插入后再删除，视图逐键恢复原样。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race ./prefixsum/

# 查看每步输入、前缀和与判定依据日志
go test -v -run TestScenarioVsNaive ./prefixsum/
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
