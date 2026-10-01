# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 块索引分隔键构建器（`ontology` 包）

键为字节串，按字节序（`bytes.Compare`）比较。`ontology.Builder` 登记相邻数据块，
为每块生成一个尽量短的分隔键（separator），用于按键快速定位所在块。

### 分隔键生成规则

- `AddBlock(last, next)`：`last` 为本块最大键，`next` 为下一块最小键。
  - 令 `d` 为 `last` 与 `next` 的最长公共前缀长度；
  - 若 `d == len(last)` 或 `d == len(next)`（其一为另一前缀），则 `sep = last`；
  - 否则令 `b = last[d]`，仅当 `b < 0xFF` **且** `b+1 < next[d]`（严格小于）时，
    `sep = last[:d] + [b+1]`；否则 `sep = last`。
  - 例：`("abcdefg","abzzz") -> "abd"`；`b+1 == next[d]` 时不缩短，仍取 `last`。
- `Finish(last)`：登记最后一块（无下一块）。取 `last` 中第一个不等于 `0xFF`
  的字节加 1 并截断其后内容；全部字节均为 `0xFF` 时 `sep = last`。

### Seek 语义

- `Seek(key)` 在 `Finish` 之后可用，返回**第一个满足 `sep >= key` 的块下标**；
  `key` 大于全部分隔键时返回 `ontology.SeekOutOfBound`（`-1`）。
- 块 `i` 覆盖区间 `[sep[i-1], sep[i])`（首块下界为负无穷），
  任何落在块最大/最小键范围内的键都会 Seek 到其真实所在块。
- `Seps()` 返回全部分隔键的逐字节副本。

### 拒绝原因（按顺序只报第一个，被拒绝操作不改变已登记内容）

- `AddBlock`：空键（先 `last` 后 `next`，`ErrEmptyKey`）→
  `last >= next`（`ErrLastNotBeforeNext`）→
  本块 `last` 小于上一块 `next`（`ErrOutOfOrderBlock`）→
  已 `Finish`（`ErrAlreadyFinished`）。
- `Finish`：空键 → 小于上一块 `next` → 已 `Finish`。
- `Finish` 之前调用 `Seek` 返回 `ErrNotFinished`。

### 并发与确定性

`AddBlock` / `Finish` / `Seek` / `Seps` 由读写锁保护，可并发调用，
结果等价于某个串行顺序；相同登记序列重放得到逐字节相同的分隔键。
始终满足 `last <= sep`，且非最后块 `sep < next`。

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
go test -run TestNaiveDifferential -v ./ontology   # 2000 组随机序列对拍，含输入/输出/判定依据日志

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
