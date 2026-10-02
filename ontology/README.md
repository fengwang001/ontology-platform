# ontology — Dremel 风格嵌套记录列式拆分/重装器

包 `ontology` 把嵌套记录（`map[string]any`）按模式拆到各叶子列，每个叶子条目带
**重复级（repetition level）** 与 **定义级（definition level）**；列按记录边界分页，
并能从各列无损重装出规范化后的原记录。

## 模式

`Schema` 是顶层 `Field` 列表：`Field{Name, Rep, Children}`。

- `Rep` 为 `Required` / `Optional` / `Repeated`。
- `Children` 为空即叶子（值类型 `int64`），否则为分组。
- 约束：至少 1 个叶子、至多 16 个叶子、嵌套深度至多 6、同级名字非空且互不相同，
  违反则 `New` 返回可 `errors.Is(err, ErrSchema)` 的错误。
- 叶子列名是点号连接的路径，例如 `author.ids`。
- `New(schema, pageEntries, maxEntries)` 要求 `pageEntries >= 1`、`maxEntries >= 1`，
  否则返回可 `errors.Is(err, ErrParam)` 的错误。

记录形态：

- 叶子取 `int64`；分组取 `map[string]any`；`Repeated` 字段取 `[]any`。
- `nil` 或空切片等价于零个元素；切片元素为 `nil`、分组元素不是 `map[string]any` 报 `ErrType`。
- `Optional` 缺键或 `nil` 表示缺失；`Required` 缺键或 `nil` 报 `ErrMissingRequired`。
- 分组中出现模式没有的键报 `ErrUnknownField`。

所有校验错误都可 `errors.Is` 到种类（`ErrSchema/ErrParam/ErrType/ErrMissingRequired/
ErrUnknownField/ErrTooLarge/ErrInconsistentCols`），并用 `ErrorPath(err)` 取点号路径。

## 两个级别

对叶子路径（自根到叶）：

- `maxRep` = 路径上 `Repeated` 节点的个数。
- `maxDef` = 路径上“非 `Required`”节点（`Optional` 与 `Repeated`）的个数。

`Shred(rec)` 沿每个叶子路径自根向下处理：

- `def` 为路径上已确认存在的非 `Required` 节点数。
  - 遇到缺失的 `Optional`，或元素数为零（nil/空切片/缺键）的 `Repeated`：
    写一个空条目（`def` 取当前值）后结束该列对本记录的遍历。
  - 存在的 `Optional`：`def` 加 1 后继续。
  - `Repeated`：逐个元素处理，每个元素 `def` 加 1 后继续。
- `rep`：
  - 新记录在该列的第一个条目 `rep=0`。
  - 某 `Repeated` 节点的第 `j`（`j>=2`）个元素之下的第一个条目，
    `rep` 为该节点的重复层序号（自根起第几个 `Repeated`，从 1 计）。
  - 其余首条目继承外层传下的 `rep`。

例（`id` 必填叶子；`author` 可选分组含 `name` 可选叶子、`ids` 重复叶子；
`tags` 重复分组含 `k` 必填叶子、`vals` 重复叶子）：

- `author.ids`：`author` 缺失 → `(0,0,空)`；`author` 存在而 `ids` 空 → `(0,1,空)`；
  `ids=[5,6]` → `(0,2,5)`、`(1,2,6)`。
- `author.name`：`author` 存在而 `name` 缺失 → `(0,1,空)`；`name=7` → `(0,2,7)`。
- `tags.vals`（`maxRep=2,maxDef=2`）：
  `tags=[{k:1,vals:[10,11]},{k:2}]` → `(0,2,10)`、`(2,2,11)`、`(1,1,空)`；
  `tags` 缺失 → `(0,0,空)`。

条目结构为 `Entry{Rep, Def, Value}`；`Value` 仅在 `Def == maxDef` 时有效。

## 校验次序

沿模式深度优先：每到一个分组，先检查是否有不在模式中的键（有则 `ErrUnknownField`，
取字节序最小的键），再按声明次序逐个检查子字段；字段存在则先深入其内部，再看下一个
同级字段。全部通过后，若任一列本记录产生的条目数 `> maxEntries`（恰等通过），
报 `ErrTooLarge`（路径为该叶子列）。被拒绝的 `Shred` 不改变任何列、分页与记录计数
（先在暂存区生成全部条目，校验通过后在一把写锁内原子提交）。

## 分页

每个列独立分页。列的当前页条目数在“追加下一条新记录的首条目之前”检查：
达到 `pageEntries`（恰等即算）就关闭当前页并新开一页。页绝不切开一条记录，
所以单条记录条目很多时页可以超过 `pageEntries`。

`Pages(col)` 返回 `[]PageInfo`：`StartRecord`（起始记录序号，从 0 起）、
`RecordCount`、`EntryCount`。例：`pageEntries=3`，列 `author.ids` 依次收到
1、1、2、… 个条目：第三条记录开始前页内 2 条（<3）不换页，页累计到 4 条；
第四条记录开始前 4（>=3）换页。

`Stats(col)` 返回空条目数（`Def < maxDef`）、非空个数、最小值、最大值。

## 重装 Assemble

`Assemble()` 按记录序号返回所有记录：

- `rep == 0` 开启新记录；`rep == k` 表示在第 `k` 个 `Repeated` 节点的列表上追加新元素。
- `def` 决定路径上哪些节点存在；缺失的 `Optional`、空的 `Repeated` 不生成，
  存在的 `Optional` 分组即使其下叶子全缺也保留为空 `map`。
- 不同列对同一重复分组元素数的结论由 `rep/def` 保证一致（条目按列 DFS 次序、
  记录内逐列重放，重复层元素下标跨列对齐）；矛盾时报 `ErrInconsistentCols`。
- 结果等于对原记录的规范化。

`entriesRead`（通过 `EntriesRead()` 读取）记录一次 `Assemble` 读取的条目总数，
恰等于各列条目之和。

## 并发与确定性

所有方法均可并发调用；`Shred` 是单个原子步骤，观察者看不到只追加了一部分列的
中间状态。相同操作序列重放产生完全相同的列条目、分页与重装结果。

## API

- `New(schema, pageEntries, maxEntries) (*Shredder, error)`
- `(*Shredder).LeafNames() []string`
- `(*Shredder).Shred(rec map[string]any) error`
- `(*Shredder).Entries(col string) ([]Entry, error)`
- `(*Shredder).Pages(col string) ([]PageInfo, error)`
- `(*Shredder).Stats(col string) (Stats, error)`
- `(*Shredder).Records() int`
- `(*Shredder).Assemble() ([]map[string]any, error)`
- `(*Shredder).EntriesRead() int`
- 错误辅助：`ErrorPath(err) string`

## 本地验证

```bash
go test ./...
go test -race -v ./ontology
go test -run TestRandom2000 -v ./ontology   # 2000 组随机模式/记录与朴素模型对照
gofmt -l .
go vet ./...
```

`TestRandom2000` 对每组随机模式与记录：用独立的朴素模型逐列核对 `rep/def` 条目、
独立重放核对分页，并核对 `Assemble` 结果等于规范化原记录；每组在测试日志中打印
输入（模式、记录数、叶子、页大小）、输出与判定依据（`verdict=PASS`）。
