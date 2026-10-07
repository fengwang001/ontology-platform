# 常量表达式求值器设计说明

`consteval` 包实现静态语言的常量表达式求值：调用方构造结构化表达式树，
求值器按"无类型任意精度 + 有类型逐步可表示性检查"的语义求出值、种类与类型，
并支持登记命名常量供后续表达式引用。

## 模块划分

| 文件 | 职责 |
| --- | --- |
| `consteval/types.go` | 种类（`Kind`）、类型（`Type`）、类型范围、默认类型、内部运算类别 |
| `consteval/errors.go` | 错误分类（`ErrKind`）与优先级注释、统一错误类型 `Error` |
| `consteval/value.go` | 常量值 `Const` 的表示（无类型大数 / 有类型标量）、不可变访问器 |
| `consteval/expr.go` | 表达式树 `Node`/`Op`/`Lit`、构造器、结构校验、名字收集 |
| `consteval/convert.go` | 可表示性：类型转换、截断/越界判定、有理数→float64 最近偶舍入 |
| `consteval/eval.go` | 求值核心：求值顺序、节点级错误优先级、无类型/有类型运算 |
| `consteval/registry.go` | 命名常量登记、查询、并发串行化 |

依赖方向：`types/errors` ← `value` ← `expr` ← `convert` ← `eval` ← `registry`，无环。

## 关键取舍

- **值表示**：无类型整数用 `big.Int`、无类型有理数用 `big.Rat`（精确、不做舍入）；
  有类型整数用 `int64`/`uint64`、浮点用 `float64`。有类型运算统一在 `big.Int`
  中算出精确结果再做范围检查——牺牲少量性能换取"无回绕"语义的显而易见正确性
  （覆盖 `int64` 边界、移位、乘法的所有溢出路径）。
- **错误优先级**：单节点内按 类型不匹配 → 非法操作 → 除零 → 不可表示
  （越界/截断/常量过大）检查；整棵树按从左到右、先子后父返回第一个错误。
  结构非法（`ErrInvalidArgument`）在求值前对整树预检，因此优先于一切求值错误；
  未知名字也在求值前预扫描，因此优先于所有求值错误但低于重复登记。
  被逻辑运算"短路"掉的分支照常求值，错误照常报告。
- **浮点舍入**：有理数→float64 用纯 `big.Int` 运算实现最近偶舍入
  （`ratToFloat64`）：先求二进制指数，再按 53 位有效数字（次正规区固定
  2^-1074 量子）做并列取偶；结果指数超过 1023（含并列落到 2^1024 的情形）
  判越界。不依赖 `big.Float`，避免次正规区的二次舍入问题；测试中与
  `big.Float`（53 位、ToNearestEven）在正常区间做了 5000 例随机对照。
- **并发**：单个 `sync.RWMutex`。`Register` 全程持写锁（校验+求值+存储原子完成），
  `Eval`/`Lookup` 持读锁，因此所有调用的效果等价于某个串行顺序。求值只读
  已登记的不可变值。
- **查找复杂度**：登记时把值求定并以 `map[string]*Const` 存储，引用即一次
  哈希查找（均摊 O(1)），不存在引用链回溯——链深 10^5 的常量引用与单常量
  引用同价。由 `TestLookupFlatCost`（10^5 次深链引用限时完成）与
  `BenchmarkLookupByRegistrySize`（不同登记规模下基准对比）可验证地证明。

## 语义裁定（规范有歧义处的设计决定）

- **未写类型的登记**：常量仍以无类型存储（引用时表现为无类型常量），但登记是
  "需要具体类型的语境"，值须可表示于种类的默认类型（整数→int64、有理数→
  float64、布尔/字符串→各自类型），否则报不可表示错误。表达式求值结果已是
  有类型常量时按原样存储。
- **比较与逻辑运算的结果**：一律为无类型布尔常量（与 Go 类似），因此"有类型
  运算逐步可表示"只约束算术、位运算、移位与字符串加法。
- **float64 除零**：与其它类型一样报除零错误，不产生 ±Inf；有类型浮点运算
  结果为 ±Inf（如 `1e308*2`）判越界。NaN 无法产生（无 NaN 字面量、除零已拦截）。
- **移位**：右操作数与左操作数遵守同一套混合规则（两个有类型操作数类型必须
  完全一致；无类型计数转换到左操作数类型）；计数须为整数种类且在 [0, 1000]，
  否则为非法移位（属非法操作类）。有理数种类即使值为整数也不能参与移位。
- **512 位限制**：只约束无类型整数（|x| 的位长 ≤ 512，含字面量与每个中间
  结果）；无类型有理数的分子分母不设限。
- **bool 的比较**：只允许 `==`/`!=`，次序比较为非法操作。

## 被放弃的方案

- **回绕（wraparound）有类型运算**：与"每步可表示、越界即错"的要求冲突，放弃。
- **登记时存表达式树、引用时递归求值**：引用开销随链深增长，违反 O(1) 要求；
  且"登记时求定并固定"的语义更直白，放弃。
- **逻辑运算短路**：规范明确要求所有子表达式一律求值、错误照常报告，放弃。
- **用 `big.Float` 做有理数→float64 舍入**：次正规区存在二次舍入（先 53 位、
  再按 float64 指数范围舍入）导致并列取偶在边界上出错，改为手写精确舍入。
- **细粒度锁（每常量一把锁/无锁 map）**：串行化论证复杂而收益不明；单
  RWMutex 的"等价于某个串行顺序"由读写锁语义直接保证，放弃。
- **节点内并发求值**：求值是 CPU 轻量操作，引入并发只会破坏"从左到右第一个
  错误"的确定性，放弃。

## 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache

# 全量测试（含竞态检测）
go test -race ./consteval/

# 定向语义测试
go test -run 'TestDivision|TestShift|TestErrorPriority|TestInt512' -v ./consteval/

# 浮点舍入（并列取偶、舍入到无穷、次正规、随机对照）
go test -run 'TestFloat|TestRatToFloat64|TestIntConversion' -v ./consteval/

# 随机表达式 vs 独立朴素模型（日志含每次输入/输出/判定依据与随机种子）
go test -run TestRandomAgainstNaiveModel -v ./consteval/

# 并发串行性
go test -race -run TestConcurrentRegisterEval -v ./consteval/

# 查找复杂度证明（限时测试 + 不同规模基准）
go test -run TestLookupFlatCost -v ./consteval/
go test -run xxx -bench BenchmarkLookupByRegistrySize ./consteval/

# 静态检查
gofmt -l consteval/ && go vet ./consteval/
```
