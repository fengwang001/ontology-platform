# defassign — 编译器前端确定赋值检查

对带顺序、条件分支、循环、提前跳出与「被保护体 + 处理分支 + 清理区域」异常
保护结构的程序，判定：

- **可能未赋值读取**（`maybe-unassigned`）：读取点存在某条可达路径，变量在
  该路径上尚未赋值；诊断依据列出每条这样的路径标签（witness）。
- **无效赋值**（`dead-assign`）：一次赋值之后，在所有从它出发的路径上、
  被再次赋值之前都没有真正执行的读取。

判定口径是「**所有路径都已赋值**」，不是「存在路径已赋值」。

## DSL（每行一条语句）

| 语句 | 含义 |
| --- | --- |
| `var x y ...` | 声明变量 |
| `assign x` | 对 x 赋值 |
| `use x` | 读取 x |
| `if` / `iftrue` / `iffalse` | 不定 / 恒真 / 恒假条件，两侧用 `else`、`end` 配对 |
| `loop` / `loop1` | 至少执行零次 / 至少一次的循环 |
| `break` / `return` | 跳出循环 / 提前返回（都会先执行栈上 try 的清理区域） |
| `try` … `handler` … `cleanup` … `end` | 异常保护结构；可有 0..N 个 `handler` |

`#` 开头与空行被忽略。

## API

```go
import "ontology/defassign"

rep, err := defassign.CheckText("prog1", src) // 登记 + 校验 + 检查
if err != nil { /* *defassign.InputError */ }
fmt.Println(rep.Text())                       // 字节稳定的诊断列表

cr, err := defassign.CheckDetailed(prog)      // 额外含复杂度计数
_ = cr.ReadLaneHits                           // 每个读取点处理的车道数
```

`Check` / `CheckText` 不持有跨程序的可变状态，可对多个独立程序并发调用；
同一程序重复检查输出逐字节相同。

`NaiveCheck` 是独立的朴素路径枚举模型（枚举两侧分支、循环展开 0..4 次、
try 逐边界抛出），与传播器不共享分析代码；随机对照测试要求两者诊断集合
（含路径依据）完全一致。

## 输入错误（按此拒绝次序，整体拒绝、无部分诊断）

1. 结构树存在环（`structure cycle`）
2. 引用未声明变量（`undeclared variable`）
3. `break` 不在任何循环内（`break outside loop`）
4. `handler` 不属于任何保护结构（`handler outside protect structure`）

## 本地验证

```bash
go test ./...                  # 单元 + 随机对照 + 复杂度 + 并发
go test -race ./...
go vet ./...
gofmt -l .
go run ./cmd/defassign a.da b.da   # 打印每条输入、输出与判定依据
```

详见 `DESIGN.md`。
