# 区间树时钟副本注册表（`itc` 包）

`itc` 包实现了区间树时钟（Interval Tree Clock）的命名副本注册表：支持副本
派生（Fork）、记事件（Event）、只含事件的快照（Peek）、合并退出（Join）与
因果比较（Compare）。所有树始终保持规范化形态，因此相同操作序列重放会得到
逐字符相同的字符串输出。

## 数据形态与字符串表示

副本戳写作 `(id;event)`，内部无空格：

- 身份树：`0`、`1` 或 `(l,r)`。
- 事件树：非负整数 `n` 或节点 `(n,l,r)`。

例如 `((1,0);(1,1,0))` 表示身份为 `(1,0)`、事件为 `(1,1,0)` 的副本戳。

## 规范化

- 身份树规范化：`(0,0)` 化为 `0`，`(1,1)` 化为 `1`。所有构造（包括
  `fork`、`sum` 的结果）都经过该规则，保证身份树的唯一表示。
- 事件树运算：`min(n)=n`，`min(n,l,r)=n+min(min(l),min(r))`；`max` 同理
  取较大者；`lift(m,e)` 对整数为 `e+m`，对节点为首分量 `n+m`。
- 事件树规范化 `norm(n,l,r)`：
  1. 若 `l`、`r` 都是整数且相等（设为 `m`），化为整数 `n+m`；
  2. 否则令 `m = min(min(l),min(r))`，化为
     `(n+m, l−m, r−m)`（对子树减 `m` 即对其首分量或整数自身减 `m`）。

规范化后每个事件节点的两子树最小值至少一个为 `0`，测试
（`TestRandomDifferential` 中的 `checkEventNorm`）会显式校验这一不变量。

## 操作语义

### Seed / Fork

- `Seed(name)` 建立戳 `(1;0)`。
- `Fork(name, child)` 调用 `fork` 拆分身份：name 持前半、child 持后半，二者
  共享同一份事件树。`fork` 规则按规范中的次序匹配：
  `fork(0)=(0,0)`；`fork(1)=((1,0),(0,1))`；单侧为 `0` 时向另一侧递归；
  两侧都非 `0` 时 `fork((i1,i2))=((i1,0),(0,i2))`。

### Event：fill 与 grow 的取舍

`Event(name)` 先在当前身份与事件树上求 `fill`：

- 若 `fill` 结果与原事件树不同，则采用 `fill` 结果（先补齐身份可写区域）；
- 否则采用 `grow` 结果并规范化（在代价最小的位置推进事件）。

`fill` 规则严格按列出次序取第一个匹配项，例如
`fill((1,ir),(n,el,er))` 先于通用的
`fill((il,ir),(n,el,er))`；其中
`fill((1,ir),(n,el,er)) = norm(n, max(max(el), min(er')), er')`，
`er' = fill(ir,er)`，另一侧对称。

`grow` 返回 `(新事件树, 代价)`：

- `grow(1,n) = (n+1, 0)`；
- 单侧身份为 `0` 时沿另一侧递归，代价 `+1`；
- 两侧都非 `0` 时分别求 `(el',cl)`、`(er',cr)`，`cl<cr` 取左侧、
  否则（含 `cl==cr` 平票）取右侧，代价 `+1`；
- 当事件是整数 `n`、身份是节点时，把 `n` 视为**未规范化**的原始节点
  `(n,0,0)` 再递归，最终代价额外 `+1000000`。该罚则每次整数展开只计一次，
  由 `evGrow` 在外层统一加收。

### Peek

`Peek(name)` 返回 `(0;event)`：身份抹去为 `0`，仅暴露事件。接口直接返回
字符串快照，不暴露内部指针，因此快照不可能与后续内部状态别名。

### Join

`Join(a,b)`：

- 身份取 `sum(ia,ib)`：`sum(0,i)=i`、`sum(i,0)=i`、
  `sum((l1,r1),(l2,r2))` 为规范化后的两侧求和；其余组合（含 `1` 与任意
  非 `0` 身份）构成身份重叠，整体拒绝。
- 事件取 `join(ea,eb)`：整数间取 `max`；整数与节点混合时把整数 `n` 视为
  未规范化节点 `(n,0,0)`；两节点 `(n1,l1,r1)`、`(n2,l2,r2)` 先保证
  `n1<=n2`（否则交换），再计算
  `norm(n1, join(l1, lift(n2−n1,l2)), join(r1, lift(n2−n1,r2)))`。
- 成功后 `b` 被原子移除，此后任何对 `b` 的操作都返回未知名字。

### Compare

`Compare(a,b)` 只比较事件，不看身份：

- `leq(n1,n2) = n1<=n2`；整数对节点只比较首分量；
- `leq((n1,l1,r1), n2)` 要求 `n1<=n2` 且左右子树经 `lift(n1,·)` 后也都
  `leq n2`；
- 两个节点比较时把两侧子树分别 `lift` 到统一基线再比较。

结论四选一：`Before`（a≤b 且非 b≤a）、`After`、`Equal`、`Concurrent`。

## 拒绝规则与顺序

所有错误都通过哨兵错误返回，可用 `errors.Is` 区分：
`ErrEmptyName`、`ErrNameExists`、`ErrUnknownName`、`ErrSameName`、
`ErrIdentityOverlap`。

`Fork`、`Join`、`Compare` 按以下顺序只报第一个错误：

1. 任一名字为空（Compare 无“同名”与“重叠”两项）；
2. 两个名字相同（仅 Join）；
3. 第一个名字未知；
4. 第二个名字：Fork 报“已存在”，Join/Compare 报“未知”；
5. 身份重叠（仅 Join）。

被拒绝的操作不会修改任何副本状态。

## 并发与不变量

`Registry` 用互斥锁保护，`Seed/Fork/Event/Peek/Join/Compare/String`
均可并发调用，其结果等价于某个串行顺序。从一次 `Seed` 出发的任意
Fork/Event/Join 序列中，所有存活副本的身份按 `sum` 合并恒为 `1`，且两两
不重叠；`TestConcurrent` 在 `-race` 下验证这一点，
`TestRandomDifferential` 在每一步后校验该不变量。

## 本地验证

```bash
# 全量测试（2000 组随机序列与朴素参考实现逐条对照，日志含输入/输出/判定）
go test ./itc/

# 竞态检测与详细日志
go test -race -v ./itc/

# 仅运行随机差分用例并查看输入、输出与 MATCH 判定
go test -run TestRandomDifferential -v ./itc/

# 格式化与静态检查
gofmt -l .
go vet ./...
```

测试构成：

- `itc_test.go`：规范示例序列、fill 三个节点分支、grow 平票取右、
  `+1000000` 整数事件代价、Compare 四种结论。
- `validation_test.go`：拒绝原因、报错顺序、拒绝操作不改状态、Join 后
  未知名字、Peek 快照。
- `naive_test.go`：按规范公式逐条直写的朴素参考实现（独立的
  interface 树表示，与生产代码不共享结构）。
- `diff_test.go`：2000 组随机 Fork/Event/Join 序列差分对照（打印输入、
  双方输出与 `MATCH/MISMATCH` 判定）、事件树规范化不变量、存活身份求和
  为 `1` 不变量、重放逐字符一致、并发 `-race` 场景。
