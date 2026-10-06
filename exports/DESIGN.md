# 导出映射解析器设计说明

`ontology/exports` 实现模块包导出映射（exports map）的解析：给定导出映射表与一次
导入请求（子路径 + 活动条件集合），决定请求落到哪个内部目标，或给出可区分的错误。

## 模块划分

| 文件 | 职责 |
| --- | --- |
| `errors.go` | 错误类别 `ErrorKind` 与统一错误类型 `Error`，`IsKind` 判定 |
| `target.go` | 目标数据模型：字符串 / 显式禁止 / 有序条件映射（可嵌套） |
| `index.go` | 子路径查找索引：精确键哈希表 + 通配键拆分对哈希表 |
| `table.go` | 表的构造校验（不可变快照）与纯函数式解析算法 |
| `resolver.go` | 并发安全解析器：`atomic.Pointer` 持有当前表，支持整表原子替换 |

协作关系：`Resolver` 持有 `Table`，`Table` 组合 `index` 做键选择，再用
`resolveTarget` 递归解析 `Target`，所有错误统一为 `*Error`。

## 关键取舍

### 1. 通配键查找：拆分对哈希表（采用）

把每个通配键拆成 `(prefix, suffix)` 存入哈希表。解析时枚举请求子路径的全部拆分
`subpath = prefix + matched + suffix`（`matched ≥ 1` 字符），按"前缀最长、其次整键
最长"的优先级顺序探测，第一个命中即最优。

- 探测顺序本身就是裁决顺序，无需事后排序或比较。
- 探测次数上界为 `1 + n(n+1)/2`（n 为请求子路径长度），**与键总数无关**。
- 正确性由 `TestRandomizedAgainstNaiveModel` 对拍保证；复杂度由
  `TestLookupProbesIndependentOfTableSize` / `TestLookupProbesMissBound` 以
  探针计数方式直接验证（12 键与 20002 键的表探测次数完全相同）。

### 2. 被放弃的方案

- **逐键扫描**：O(键数)，违反"开销不得随键总数增长"，仅保留为测试中的朴素参照模型。
- **按键长排序 + 二分**：匹配同时依赖请求的前缀与后缀两个维度，单一排序键无法
  剪枝后缀维度，最坏仍退化为 O(键数)。
- **前缀 Trie + 节点内后缀列表**：大量通配键共享前缀时（如 `./a*1 … ./a*N`），
  命中节点后仍需线性检查后缀，最坏 O(键数)。
- **`sync.RWMutex` 保护可变表**：读临界区虽短，但替换方需等待所有读者，且实现
  上容易误改共享状态。改用不可变表 + `atomic.Pointer`：替换先完整构造并校验新表，
  再一次 `Store` 发布；解析方一次 `Load` 即拿到一致的整表快照，等待-free。

### 3. 不可变表与原子替换

`Table` 构造后无任何修改路径（条件切片进出均拷贝）。`Resolver.Replace` 先走完整
的 `NewTable` 校验，失败则直接返回 `KindInvalidTable`，旧表继续生效；成功才
`Store`。线性化点：`Store`/`Load` 各自是原子操作，因此每次解析完整看到旧表或
新表之一，结果等价于某个串行顺序，绝不混用两张表。

### 4. 目标字符串合法性在解析时校验

表非法规则是封闭清单，不含目标字符串合法性；且通配目标必须代入匹配段后才能判定
（匹配段可能把 `node_modules`、`..` 等非法段带入目标）。因此字符串目标一律在解析
时、星号替换后校验，不合法报 `KindInvalidTarget` 且不回退。

### 5. 解释性决策（规范未逐字覆盖处）

- 段合法性检查作用于 `"./"` 前缀**之后**的各段（否则任何合法目标的首段 `"."`
  都会误判）；空段（如 `./a//b`）不在禁止清单内，予以放行。
- 请求的活动条件按集合处理（去重）；条件名为空串视为请求非法。
- 选中键不含星号时，目标中的 `*` 不做替换，按字面字符参与校验。

## 错误类别与优先级

`KindInvalidRequest`（请求非法）> `KindNotExported`（子路径未导出）> 解析路径上
先到先得：`KindForbidden`（被禁止）、`KindNoMatchingCondition`（无匹配条件）、
`KindInvalidTarget`（非法目标）。`KindInvalidTable` 只会在构造或替换时出现。
被禁止与非法目标一旦命中立即上报，不再尝试后续条件；只有"内部无匹配条件"会
触发对后续条件的回退重试。

## 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache

go build ./...                 # 编译
go vet ./exports/              # 静态检查
gofmt -l exports/              # 格式（无输出即通过）
go test ./exports/             # 全部测试
go test -race ./exports/       # 竞态检测（含并发替换/解析交错）
go test -v -run TestRandomizedAgainstNaiveModel ./exports/  # 随机对拍，逐条打印输入/输出/判定依据
go test -v -run TestLookupProbes ./exports/                 # 复杂度探针验证
```

## 测试覆盖对照

| 需求 | 测试 |
| --- | --- |
| 精确键与通配键并存 | `TestExactBeatsWildcard` |
| 前缀等长按键长裁决 | `TestWildcardTieBreakByKeyLength` |
| 匹配段含斜杠 / 为空 | `TestWildcardMatchedSegmentWithSlash` / `TestWildcardEmptyMatchFails` |
| 条件嵌套与默认条件 | `TestNestedConditionsAndDefault`、`TestInnerNoMatchFallsThrough` |
| 禁止与无匹配的区别 | `TestForbiddenVsNoMatch`、`TestForbiddenStopsFallback` |
| 替换后星号落入非法段 | `TestReplaceAtomicityAndRejection`、`TestMatchedSegmentCarriesIllegalSegment` |
| 表替换与解析并发交错 | `TestConcurrentReplaceAndResolve`（`-race`） |
| 朴素模型随机对照 | `TestRandomizedAgainstNaiveModel`（400 表 × 8 请求，逐条日志） |
| 开销与键总数无关 | `TestLookupProbesIndependentOfTableSize`、`TestLookupProbesMissBound` |
| 表非法各规则 | `TestTableValidation` |
| 请求非法优先 | `TestInvalidRequestBeatsNotExported` |
