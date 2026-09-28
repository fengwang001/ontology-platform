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

## 事务标识集合组件（`txnset`）

`txnset` 包用于复制断点续传：把**源端已执行事务集合**与**本地已执行
事务集合**相减，得到尚待回放的事务集合并输出规范文本。任意写法、顺序、
重叠的合法输入都得到唯一（逐字节相同）的规范结果。

### 文本语法

文本由逗号分隔的条目组成（空文本表示空集合），**不允许任何空白字符**：

```text
集合    = 条目 *( "," 条目 ) / ""
条目    = 来源 ":" 事务号 [ "-" 事务号 ]
来源    = ( 字母 / "_" ) *( 字母 / 数字 / "_" )   ; 不超过 32 字节
事务号  = "0" / ( 非零数字 *数字 )                ; int64 范围内，无前导零
```

- `src:N` 表示单点闭区间 `[N,N]`；`src:N-M` 表示闭区间 `[N,M]`（`N<=M`）。
- 来源支持 Unicode 字母（如 `α源:1`），长度按字节计。
- 示例：`primary:9,replica:1-6`

### 规范化规则

1. 同一来源的区间取并集，合并所有**重叠或相邻**的区间
   （相邻指前一区间 `Hi+1 ==` 下一区间 `Lo`，如 `1-3` 与 `4-6` 合并为 `1-6`）。
2. 输出时来源按**字节序升序**排列；同一来源内区间按下限升序。
3. 单点区间渲染为 `N`，闭区间渲染为 `N-M`。
4. 规范文本再次解析结果逐字节不变（幂等）。

### 差集规则

`source.Diff(local)` 按来源分别做闭区间减法，返回**新集合**，不修改任何输入：

- 来源只在源端出现时整段保留；两端都有的来源才做区间减法。
- 闭区间端点精确：`[1,5]-{1}-{5} = [2,4]`；
  `[1,100]-[1,60]-[62,80] = {61} ∪ [81,100]`。
- 被内部点切开的片段不会重新相邻：`[1,5]-{3} = [1,2] ∪ [4,5]`。

### 错误分类

解析从左到右扫描，报告**第一个**错误并给出其字节偏移；一段文本中
前段合法、后段非法时整体失败，前段也不会并入。四类错误可用 `errors.Is`
区分：

| 错误哨兵 | Kind | 触发场景 |
| --- | --- | --- |
| `ErrSyntax` | `KindSyntax` | 空白、空条目、缺冒号、缺上下限、多余 `-` |
| `ErrIdentifier` | `KindIdentifier` | 来源首字符非法、含非法字符、超过 32 字节 |
| `ErrNumber` | `KindNumber` | 数字含非法字符、前导零、超出 int64、下限大于上限 |
| `ErrTooManyIntervals` | `KindTooManyIntervals` | 输入条目数超过 `MaxIntervalCount`（默认 10000） |

```go
s, err := txnset.Parse(text)
var pe *txnset.ParseError
if errors.As(err, &pe) {
    // pe.Kind / pe.Offset / pe.Message
}
errors.Is(err, txnset.ErrNumber) // 按类别判定
```

### 并发语义

- `Parse`、`Merge`、`MergeText`、`Diff` 及所有读方法均可并发调用。
- 合并由互斥锁串行化；差集先快照两个操作数再计算，持锁期间不嵌套加锁。
- 同一逻辑集合无论如何切分、以何种顺序并发合并，`Canonical()` 输出
  逐字节一致；`Diff` 绝不改变任何输入状态。
- 可用 `txnset.SetLogger(slog.New(...))` 打开结构化日志，日志中包含
  输入文本、规范文本与判定依据（basis）；传 `nil` 关闭。

### 命令行工具

```bash
go run ./cmd/txnset canonical 'replica:5,replica:1-3,primary:9,replica:4-6'
# -> primary:9,replica:1-6

go run ./cmd/txnset diff 'db:1-100' 'db:1-60,db:62-80'
# -> db:61,db:81-100

go run ./cmd/txnset --log canonical 'b:2,a:3-5,a:1'   # 附带判定日志
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./txnset
go test -run TestDiffEndpoints ./txnset
go test -run Example ./txnset

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
