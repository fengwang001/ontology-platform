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

## 事务标识集合组件（`txnset`）

用于复制断点续传：把“源端已执行事务集合”与“本地已执行事务集合”相减，
得到仍需补偿的事务，并输出唯一、逐字节确定的规范文本。任意写法、顺序、
相互重叠或相邻的输入都得到同一结果。

### 文本语法

```text
文本 = 条目 (';' 条目)*
条目 = 来源 ':' 区间 (',' 区间)*
区间 = 号 | 号 '-' 号        # 后者为闭区间 [lo,hi]，要求 lo <= hi
来源 = 字母 (字母 | 数字 | '_' | '-')*
号   = '0' | 非零数字 数字*   # uint64 范围，禁止前导零
```

- **任何位置都不允许空白**（空格、制表符、换行等），空条目、多余或
  缺失分隔符同样非法。
- 错误按**从左到右报告第一个**，并带字节偏移。四类错误可区分
  （`errors.Is` 或 `ParseError.Kind`）：
  - `ErrSyntax` 语法错误（含空白、分隔符问题）
  - `ErrIdentifier` 来源标识非法
  - `ErrNumber` 数值非法（前导零、uint64 溢出、区间端点倒置）
  - `ErrTooMany` 区间总数超过 `txnset.MaxIntervals`（10000）
- 整段文本全部合法后才生效：一段文本中前段合法、后段非法时，
  **前段也不会并入**。

### 规范化规则

- 同一来源的区间取并集，**合并重叠与相邻（闭区间下端点相接）区间**。
- 来源按字典序升序；每条 `来源:区间...` 之间以 `;` 连接成单行文本；
  行内区间按起点升序；单点渲染为 `n`，闭区间渲染为 `lo-hi`。
- 规范文本可被 `Parse` 原样解析（往返一致），同一逻辑集合无论以何种
  切分、顺序输入，`Canonical()` 输出逐字节相同。空集合输出空字符串。

### 差集规则

- `s.Difference(other)` 逐来源做闭区间减法，返回**新集合**；
  两个操作数的状态都不改变。
- 仅在双方都出现的来源上相减；`other` 没有的来源原样保留，
  `s` 没有的来源不产生结果。差集结果同样保持规范化。
- 端点示例：`[1,100] - {1,50,100}` = `[2,49] ∪ [51,99]`。

### 并发语义

- `Parse` 是纯函数；`Merge`、`Difference`、`Canonical` 等均可并发调用。
- `Merge` 之间互斥，且“先完整解析、再加锁一次性并入”，非法输入不留
  部分状态；并发合并互不重叠区间与一次合并结果一致。
- 多集合差集按集合唯一 id 固定加锁顺序，避免双向并发差集死锁。
- 每次解析/合并/差集都会通过 `txnset.SetLogOutput(io.Writer)` 配置的
  落点打印输入、规范文本与判定依据（默认丢弃）。

```go
source, _ := txnset.Parse("mysql-a:100-300,500;pg-b:1-9")
local, _  := txnset.Parse("mysql-a:100-250;pg-b:1-9")
fmt.Println(source.Difference(local).Canonical())
// mysql-a:251-300,500
```

### 本地验证

```bash
# 单元测试（含相邻合并、乱序重复、闭区间端点、各类非法输入、并发）
go test -race -v ./txnset/

# 覆盖率
go test -coverprofile=coverage.out ./txnset/
go tool cover -func=coverage.out

# 命令行验证规范化与差集
go run ./cmd/txnset canon 'z:9,1-2;a:5,5-8'
go run ./cmd/txnset diff 'a:1-100' 'a:50-100'   # 输出 a:1-49
```
