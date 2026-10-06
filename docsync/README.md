# docsync — 文档同步与诊断迁移器

语言服务风格的带版本文本文档内核：维护以 UTF-16 码元为列单位的 Unicode 文档，
按“同时坐标”语义原子应用一组增量编辑，并让已发布诊断随编辑**迁移**或**失效**。
纯 Go，零第三方依赖。完整设计见 [DESIGN.md](DESIGN.md)。

## 模块

| 文件 | 职责 |
| --- | --- |
| `utf16.go` | 类型（`Position`/`Range`/`Edit`/`Diagnostic`）、错误、UTF-16 码元计数与列校验（区分越界与代理对中间）。 |
| `rope.go` | 持久化 treap rope：按码元分裂/合并/替换、换行聚合、`O(log n)` 行定位与位置↔偏移、结构访问计数。 |
| `diag.go` | 诊断双 treap（按起点/按终点）、懒平移、相交失效枚举（聚合剪枝）、按 `(key,seq)` 保序合并。 |
| `document.go` | `Service`：版本与读写锁、拒绝优先级、变更应用、诊断失效与迁移、一致快照、换算 API。 |
| `naive.go` | 独立朴素参照模型（整篇 UTF-16 切片 + 诊断线性推演），仅供差分测试。 |

## 公共 API

```go
svc := docsync.NewService("初始文本")

// 变更：基版本 + 同时坐标编辑组；失败返回具体原因（见错误哨兵）。
res := svc.Change(baseVersion, []docsync.Edit{
    {Range: docsync.Range{Start: docsync.Position{0, 0}, End: docsync.Position{0, 0}}, Text: "插入"},
})

// 登记诊断（基版本必须匹配、范围合法）。
seq, err := svc.Register(version, docsync.Diagnostic{
    Range: docsync.Range{Start: docsync.Position{0, 0}, End: docsync.Position{0, 1}},
    Severity: docsync.SeverityError, Message: "说明",
})

// 同一版本的一致快照：文本、版本、有效诊断、已失效清单。
snap := svc.Snapshot()

// 位置 ↔ UTF-16 码元偏移。
off, err := svc.PositionToOffset(docsync.Position{0, 1})
pos, err := svc.OffsetToPosition(off)
```

错误哨兵（可用 `errors.Is` 区分）：

- `ErrStaleVersion`：基版本不匹配（拒绝优先级最高）。
- `ErrOutOfBounds`：行不存在或列越界。
- `ErrInsideSurrogatePair`：列落在增补字符的两个 UTF-16 码元之间。
- `ErrInvalidRange`：范围起点大于终点（登记时优先于越界判定）。
- `ErrOverlappingEdits`：同组编辑正长度区间重叠。

拒绝优先级：

- 变更：过期 → 越界 → 代理对中间 → 起点大于终点 → 编辑重叠。
- 登记：过期 → 起点大于终点 → 越界 → 代理对中间。

任一拒绝都不改变文本、版本与诊断。

## 关键语义

- 仅 `U+000A` 分行，`U+000D` 为普通字符；行数 = 换行符数 + 1，空文档一行空行。
- 一组编辑范围全部相对变更前文档；相接允许、同点多插入按给定次序串联。
- 失效：非空编辑与诊断范围正长度相交即失效；空诊断仅当严格落在删除区间 `(s,e)` 内失效；
  相接（`b==s` 或 `a==e`）不失效，重叠一码元即失效。失效记录当时版本，不可恢复。
- 迁移：起点、终点按规则独立映射；插入点四种关系（之前、`s==a`、内部、`s==b`）分别处理，
  同点多插入对起点全部生效、对非空诊断终点全部不生效、空诊断整体后移到整组之后。
- 并发：读写锁保证可线性化；同基版本的并发变更恰有一个成功。快照在同一锁内组装，不撕裂。

## 复杂度

- 变更文本：`O(m log n + 新文本)`；诊断失效/迁移：`O(m log D + k log D)`。
- 位置↔偏移：`O(log n + 块大小)`，不随总行数增长。
- 编辑点之前的文本行/诊断不被逐个访问（懒平移 + treap 路径访问）。

## 验证

```bash
export PATH=$PATH:/usr/local/go/bin

go test ./docsync/ -race -v            # 全部用例（含并发竞争、随机差分）
go test ./docsync/ -run TestRandom -v  # 200 种子 × 120 步随机差分，日志见 testdata/random.log
go test ./docsync/ -run TestPerf -v    # 访问计数：大文档/大量诊断下证明亚线性
go vet ./...
gofmt -l .
```

随机差分测试用相同操作序列同时驱动 treap `Service` 与独立朴素模型，逐版本比对
文本、版本、有效诊断（码元偏移）、失效清单、位置/偏移换算；每次输入、输出与
判定依据（接受/拒绝原因、失效集合）写入 `testdata/random.log`。

性能测试通过节点访问计数给出可核验证据（3.2 万行 vs 数千行，触达量近似常数/对数）。
