# snippet — 带文档登记的搜索结果片段选取器

`snippet` 包提供线程安全的文档登记表，以及在给定命中（hit）区间上的
固定宽度贪心片段选取，选取结果完全确定、可重放。

## API

- `NewRegistry() *Registry`
- `(*Registry).Register(docID string, text string) error`：登记 UTF-8 文本，
  最长 1048576 字节。
- `(*Registry).Unregister(docID string) error`：删除文档。
- `(*Registry).Snippets(docID string, hits []Interval, W, K int) ([]Snippet, error)`：
  选取至多 K 个互不相交片段。`Interval{Start,End}` 为字节半开区间；
  `Snippet{Start,End,Highlights,Score}` 同样以字节偏移表示。

所有方法可并发调用，效果等价于某个串行执行顺序。被拒绝的操作不改变
登记表；返回的切片均为独立拷贝，不别名内部状态；文本在登记时复制。

## 片段选取规则（逐轮）

初始已选片段集合为空。命中先按完全相同的 `(s,e)` 去重（顺序无关），
然后重复以下轮次，直到选满 K 个或规则结束：

1. **可用命中**：与全部已选片段都不相交的命中。相交判定为
   `s < 片段End 且 片段Start < e`，因此相接的 `[1,3)` 与 `[3,5)` 不算相交。
2. 对每个不同的命中起点 `a`：
   - 窗口右端 `r(a) = min(a+W, len(text), 起点大于 a 的已选片段的最小起点)`。
   - 包含集 `C(a)` = 可用命中里满足 `s >= a` 且 `e <= r(a)` 的全部命中；
     `e` 恰好等于 `r(a)` 时包含，`e = a+W+1` 不包含。
   - 分值 `score(a) = |C(a)|`，重叠的不同命中各自计一。
3. 取分值最大的 `a`；并列取最小的 `a`。若最大分值为 0
   （所有命中都比窗口宽、放不进任何窗口），结束选取。
4. 选中片段为 `[a, E)`，其中 `E = max{ e : (s,e) ∈ C(a) }`，加入已选集合。
5. 下一轮移除与该片段相交的全部命中。

返回时片段按 `Start` 升序排列（不是选取顺序）。

### 高亮区间

每个片段的 `Highlights` 是 `C(a)` 按起点升序合并后的并集区间：
仅当下一个区间满足 `s2 < e1`（严格重叠）才并入当前区间；相接
（`s2 == e1`）时保留为两个独立区间。

## 拒绝原因（按此顺序只报第一个）

| 操作 | 顺序 | RejectReason |
| --- | --- | --- |
| Register | docID 为空、text 非法 UTF-8 或超长 | `invalid_argument` |
| Register | docID 已存在 | `duplicate_document` |
| Unregister | docID 不存在 | `document_not_found` |
| Snippets | docID 未登记 | `document_not_found` |
| Snippets | W∉[1,4096]、K∉[1,16]、去重前 hits > 10000 | `invalid_argument` |
| Snippets | 命中越界、s≥e、端点落在多字节字符内部 | `invalid_hit` |

错误以 `*snippet.RejectError` 返回，用 `errors.As` 读取 `Reason`。
`hits` 为空合法，返回空结果。

## 本地验证

```bash
# 全量测试（含 2000 组随机对拍与并发测试）
go test ./snippet

# 对拍日志：逐例打印输入、输出与判定依据（MATCH/MISMATCH）
go test -run TestRandomDifferential -v ./snippet

# 竞态检测
go test -race -v ./snippet

# 格式化与静态检查
gofmt -l .
go vet ./...
```

测试组成：

- `snippet_test.go`：规则点名的边界用例（`e==a+W` 与 `a+W+1`、并列取
  最小起点、重叠命中各自计分且高亮合并、相接不合并、命中长于 W 结束、
  窗口右端被已选片段起点截短、跨边界命中失效、按 Start 排序、多字节
  字符内部端点整体拒绝、K 大于可选数、拒绝不改状态、拒绝优先级、
  无别名与顺序无关）。
- `naive_test.go`：按规格逐轮写成的朴素参考实现。
- `diff_test.go`：2000 组随机输入对拍、命中乱序顺序无关校验、相同操作
  序列重放一致性、并发 Register/Snippets/Unregister 竞态测试。
