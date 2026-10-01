# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 整数列谓词等价类推导器

`ontology` 包提供并发安全的 `Deriver`，录入 `Cmp`、`Eq`、`Ne` 谓词并维护每个等价类的闭区间 `[lo,hi]` 与被排除值集合。新列在某条合法且最终被接受的谓词首次提及时登记，初始区间为 `[MinInt64, MaxInt64]`。

### 谓词与规整

- `Cmp(column, op, c)`：`=` 令区间与点 `c` 取交；`!= c` 将 `c` 加入排除值；`< c` 令 `hi=min(hi,c-1)`；`<= c` 令 `hi=min(hi,c)`；`> c` 令 `lo=max(lo,c+1)`；`>= c` 令 `lo=max(lo,c)`。
- 严格比较在 `c == MinInt64` 的 `< c` 或 `c == MaxInt64` 的 `> c` 时直接矛盾，不做 int64 回绕。
- `Eq(a,b)` 合并等价类：区间取交，排除值取并；`Ne(a,b)` 要求两类不同。`Ne(a,a)` 以及对已有 `Ne` 对再录入 `Eq` 均为矛盾。
- 每次变化后反复规整：若 `lo` 被排除则 `lo++`，若 `hi` 被排除则 `hi--`；丢弃区间外排除值；`lo > hi`、`lo==MaxInt64` 仍需加一、或 `hi==MinInt64` 仍需减一均为矛盾。
- `lo == hi` 时该等价类被钉死，取值为 `lo`。已记录的不同对中，若一类钉死，则把该值加入另一类的排除值；两个都钉死且同值为矛盾。
- 推导只执行上述局部规则，不做多对不同关系的鸽巢式不可满足推导。

### 查询

- `Range(column)` 返回当前 `(lo,hi)`。
- `Pinned(column)` 返回 `(value,pinned)`。
- `Excluded(column)` 返回升序复制的排除值；规整后这些值严格位于 `lo` 与 `hi` 之间。
- `Same(a,b)` 返回两列是否属于同一等价类。
- `Implies` 仅接受 `Cmp` 与 `Eq`：`=` 要求同类钉死到该值；`!=` 要求常量在区间外或在排除值中；`<`、`<=`、`>`、`>=` 分别按 `hi<c`、`hi<=c`、`lo>c`、`lo>=c` 判定；`Eq(a,b)` 要求同类，或两类分别钉死到同值。

### 错误与并发

- 非法谓词返回包装了 `ErrInvalidPredicate` 的错误；矛盾返回 `ErrContradiction`；查询未登记列返回 `ErrUnknownColumn`。
- 录入时非法优先于矛盾。`Implies` 按“非法谓词（包括 `Ne`）→ 未登记列”的顺序只返回第一个错误。
- `Add` 在状态克隆上完成录入和闭包规整，只有成功后才提交；因此被拒绝的谓词不会登记列或改变任何已有状态。
- 所有录入与查询都受 `sync.RWMutex` 保护，并发执行结果等价于某个串行顺序。同一组全部可接受的谓词以任意顺序录入，最终区间、钉死值、排除值和同类划分一致。

### 本地验证

测试包含定点边界、`Eq` 合并、`Ne` 传播、拒绝回滚、2000 组随机序列与逐步朴素重放实现对拍，并在常量 `[-3,3]`、至多 3 列时枚举每列 `[-9,9]` 的所有取值验证拒绝集无解和钉死值唯一。`go test -v` 日志会打印输入、输出与判定依据。

```bash
# 如果 HOME 下的 Go build cache 只读，可显式指定缓存目录
GOCACHE=/tmp/ontology-gocache go test -v ./...
GOCACHE=/tmp/ontology-gocache go test -race ./...
GOCACHE=/tmp/ontology-gocache go vet ./...
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
