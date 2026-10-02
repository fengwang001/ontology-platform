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

## debounce：文件监视事件合并去抖器

`debounce` 包把一路原始文件事件按路径折叠成净事件，在静默期满后批量吐出，
使原子保存、创建后删除、目录删除吞并子孙等序列的折叠结果与吐出时刻可精确复现。

### 构造

```go
d, err := debounce.New(Q, W, Cap) // Q 静默期毫秒，W 最长等待毫秒，Cap 待发布条目上限
```

构造校验按顺序只报第一个错误：`Q < 1` → `ErrQuietTooSmall`，
`W < Q` → `ErrMaxWaitTooSmall`，`Cap < 1` → `ErrCapTooSmall`。

### 折叠表

每个路径至多一个待发布条目，记净类型（C/M/D）、首次时刻 `first` 与末次时刻 `last`。
`DeleteDir` 先移除所有以「该路径加 `/`」为前缀的待发布条目（不论净类型，
吞并 `a/b` 而不吞并 `ab/c`），再对该路径自身按 `Delete` 折叠。

| 现存净类型 | Create | Modify | Delete / DeleteDir |
| ---------- | ------ | ------ | ------------------ |
| 无条目     | C      | M      | D                  |
| C          | C      | C      | 取消（不发出）     |
| M          | M      | M      | D                  |
| D          | M      | D      | D                  |

新条目 `first = last = now`；对现存条目的折叠（除取消外）`last = now`、`first` 不变；
取消后条目消失，之后同路径事件按无现存条目处理。

### 到期与吐出

`Flush(now)` 吐出全部到期条目并从待发布中移除，按路径字节序升序：

- 到期当且仅当 `now - last >= Q` 或 `now - first >= W`；
- 吐出项为 `(路径, 净类型, first, last)`；
- `NextDue()` 返回各条目 `min(last+Q, first+W)` 的最小值，无条目时返回 `false`。

### 错误优先级

`Add` 按顺序只报第一个错误，被拒绝的操作不改变条目、最大 `now` 与已吐出结果：

1. `ErrUnknownKind`：种类未知；
2. `ErrInvalidPath`：路径为空、以 `/` 开头或结尾、含空段（连续 `//`）；
3. `ErrClockBackwards`：`now` 小于此前已接受的 Add/Flush 的最大 `now`；
4. `ErrCapFull`：待发布条目数已达 Cap 且该路径当前没有条目
   （此判定在 `DeleteDir` 清除子孙之前进行）。

`Flush` 只会遇到 `ErrClockBackwards`。

### 并发与确定性

`Add`、`Flush`、`NextDue` 均可并发调用，内部以互斥锁串行化，
结果等价于某个串行顺序；相同的 `(now, 事件)` 序列重放得到完全相同的吐出序列。

### 本地验证

```bash
# 单元测试（折叠表、到期边界、DeleteDir 前缀、容量、时钟回拨等）
go test ./debounce

# 与朴素参考实现对拍 2000 组随机序列，-v 打印每组的输入、输出与判定依据
go test -v -run TestDifferentialAgainstNaive ./debounce

# 竞态检测
go test -race ./debounce
```
