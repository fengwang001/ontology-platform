# vanload：厢式货车配载与卸货顺序校验

`import "ontology/vanload"`

车厢分区从车头到车尾编号 1..N；货物有编号、重量（克）、体积（立方厘米）、
卸货停靠点序号与类别（普通/食品/易燃/氧化）。系统在
**顺序、隔离、载重、容积**四类约束同时成立时给出确定的装载分区，
并在校验卸货顺序时给出可区分的失败原因。

## 快速上手

```go
sys, _ := vanload.New([]vanload.Compartment{
    {MaxWeight: 1000, MaxVolume: 2000},
    {MaxWeight: 800, MaxVolume: 1500},
}, vanload.WithLogger(vanload.NewTextLogger(fmt.Println)))

res, err := sys.Load(vanload.Cargo{
    ID: 1, Weight: 100, Volume: 200, Stop: 3, Category: vanload.CategoryFood,
})
// res.Compartment 为可行分区中编号最小者

br, err := sys.BatchLoad([]vanload.Cargo{ /* ... */ }) // 全有或全无
r, err := sys.Unload(3)                               // 按停靠点递增卸货

sys.RemainingCapacities() // 各分区剩余载重/容积（只读一致快照）
sys.Locate(1)             // 某货物当前分区
sys.ArrivedStop()         // 已到达的最大停靠点序号
```

运行演示：`go run ./cmd/vanload-demo`。

## 规则要点

- **顺序**：更早卸（停靠点序号更小）的货物不得在更靠前（编号更小）的分区；同分区可混装。
- **隔离**：易燃与氧化不同区；食品不与易燃、氧化任一类同区。
- **容量**：载重与容积恰好等于上限允许。
- **选区**：四类约束都满足的分区中取编号最小者。
- **失败归并**：每分区按“顺序→隔离→超重→超容”取首个失败原因；
  整体取所有分区中严重度最低的一类（因此有分区只差容积就报超容）。
- **批量**：按下标逐件试装，任一件失败整批无变化，报最小失败下标；批内编号重复为参数非法。
- **卸货**：只能递增；更小序号货物仍在车上时报顺序错误；空停靠点成功并推进进度；
  序号不大于已到达最大序号返回“已处理”且无变化。
- **中途装货**：开始卸货后，新货停靠点必须大于已到达最大序号，否则“停靠点已过”
  （先于四类约束判定）。
- **拒绝优先级**：参数非法 > 编号重复 > 停靠点已过 > 四类约束失败；拒绝均不改变状态。
- **并发**：所有方法可并发，等价于某串行顺序；查询只读到已完成操作的一致快照。

错误类型为 `*vanload.Reject`，字段：

- `Kind`：原因（含 `RejectKind.String()` 中文名）
- `FailedIndex`：批量失败的最小下标（单件为 -1）
- `CompartmentReasons`：四类约束失败时每个分区的首个失败原因

## 文档与测试

- 设计取舍、复杂度证明与被放弃方案见 [DESIGN.md](DESIGN.md)。
- 测试覆盖边界容量、隔离全组合、顺序约束动态失效、严重度归并、
  批量不留痕、卸货顺序错误、中途装货、并发快照、确定性重放，
  并与独立朴素模型做大量随机操作序列对照：

```bash
export PATH=$PATH:/usr/local/go/bin
go test -race -count=3 ./vanload
go test -coverprofile=cover.out ./vanload
```
