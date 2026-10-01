# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## sepkey：块索引分隔键最短化构建器

`sepkey` 包为相邻数据块生成介于两块之间且尽量短的分隔键（separator），
并据此定位任意键所在的块。键为字节串，按字节序比较。

### 分隔键生成规则

- `AddBlock(last, next)` 登记一个块：`last` 为本块最大键，`next` 为下一块最小键。
  令 `d` 为 `last` 与 `next` 的最长公共前缀长度：
  - 若 `d` 等于二者之一的长度，则 `sep = last`；
  - 否则设 `b = last[d]`，仅当 `b < 0xFF` 且 `b+1` 严格小于 `next[d]` 时
    `sep = last[:d] + [b+1]`，否则 `sep = last`。
- `Finish(last)` 登记最后一块（无下一块）：取 `last` 中第一个不等于 `0xFF`
  的字节加 1 并截断其后内容；全为 `0xFF` 则 `sep = last`。
- 任意时刻每个分隔键满足 `last ≤ sep`，且非最后块的 `sep < next`；
  分隔键严格递增，相同的登记序列重放得到逐字节相同的结果。

### Seek 语义

`Seek(key)` 返回第一个满足 `sep ≥ key` 的块下标；`key` 大于全部分隔键时
返回 `ErrOutOfRange`；`Finish` 之前调用返回 `ErrNotFinished`。
`Seps()` 返回全部分隔键的副本。

### 错误与并发

非法登记被整体拒绝且不改变已登记内容，原因可区分：
`ErrEmptyKey`（键为空）、`ErrOutOfOrder`（last 不小于 next）、
`ErrNotMonotonic`（本块 last 小于上一块 next）、`ErrFinished`
（Finish 后再登记）。`AddBlock` 与 `Finish` 按上述顺序只报第一个错误。
`AddBlock`、`Finish` 与 `Seek` 可并发调用，结果等价于某个串行顺序。

### 本地验证

```bash
# 全部测试（含 2000 组随机序列与朴素实现对拍）
go test ./sepkey

# 竞态检测 + 打印每组输入、输出与判定依据
go test -race -v ./sepkey
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
