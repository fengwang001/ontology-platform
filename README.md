# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## escape 包：按函数登记的逃逸分析摘要计算器

`escape` 包提供 `Registry`：`Register(名字, k, V, 语句列表)` 在登记时完成
分析，`Summary(名字)` 返回摘要与分析轮数，`Sites(名字)` 返回各分配点
（编号升序）与分类。所有方法可并发调用，效果等价于某个串行顺序；
分配点编号全局从 1 起严格递增、无空洞，被拒绝的登记不改变任何状态。

### 指向约束

函数内分析是与语句次序无关、字段不敏感的流不敏感指向分析，取最小不动点
（反复扫描全部语句直到无变化）。对象有三类：各分配点对象、参数对象
`P0..P(k-1)`，以及每条 `d != -1` 且被调摘要 `fresh` 为真的 `Call` 语句
各自的调用结果对象 `X`（`X` 一开始就带全局标记）。变量 `i<k` 的
`pts(i)` 初为 `{Pi}`，其余为空；`heap(o)` 与 `ret` 初为空。约束：

- `New(d)`：`pts(d)` 含该分配点；`Copy(d,s)`：`pts(d) ⊇ pts(s)`。
- `Store(d,s)`：对 `pts(d)` 内每个 `o`，`heap(o) ⊇ pts(s)`。
- `Load(d,s)`：`pts(d)` 含 `pts(s)` 内各 `o` 的 `heap(o)` 之并
  （对从未 `Store` 过的参数对象 `Load` 得到空集）。
- `Ret(s)`：`ret ⊇ pts(s)`；`Global(s)`：给 `pts(s)` 内各对象种全局标记。
- `Call(d,g,args)` 按被调摘要：`glob[i]` 为真则给 `pts(args[i])` 种全局
  标记；`ret[i]` 为真且 `d != -1` 则 `pts(d) ⊇ pts(args[i])`；`E[i][j]`
  为真则对 `pts(args[i])` 内每个 `o`，`heap(o) ⊇ pts(args[j])`；
  `fresh` 为真且 `d != -1` 则 `pts(d)` 含该语句的 `X`。`d = -1`（丢弃
  结果）时不产生任何指向与 `X`。

### 三种标记与分类优先次序

- `GLB`：全局标记种子（含 `X`）沿 `heap` 边可达的闭包（含种子）。
- `RR`：`ret` 沿 `heap` 边可达的闭包。
- `PR`：各参数对象沿 `heap` 边至少走一步可达的对象集合。

分配点分类按优先次序：在 `GLB` 内为全局逃逸，否则在 `RR` 内为返回逃逸，
否则在 `PR` 内为参数逃逸，否则为栈上。

### 摘要与自递归不动点

摘要各位：`ret[i] ⟺ Pi ∈ RR`；`glob[i] ⟺ Pi ∈ GLB`；
`E[i][j] ⟺ Pj` 是 `heap(Pi)` 的直接元素；`fresh ⟺ ret` 直接含某个
分配点对象或某个 `X` 对象。

函数调用自身时按"本轮所用摘要"计，该摘要初始全假；每轮分析得到新摘要，
与所用摘要不同则以新摘要重做整个分析，直到相同为止。摘要每位只会由假
变真，故必在 `2k+k²+2` 轮内收敛；不含自调用的函数恰分析 1 轮。

### 本地验证

```bash
go test ./escape/          # 全部单元测试
go test -race ./escape/    # 并发竞态检测
go test -v -run TestRandomAgainstNaive ./escape/  # 2000 组随机序列对照朴素模拟，打印输入/输出/判定依据
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
