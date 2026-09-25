# FINDINGS: 通配符匹配边界的三处文档/实现不一致

测试基线：`wildcard_characterization_test.go`（characterization tests，断言当前真实行为，全部通过）。
以下每条给出复现输入、实际输出、应当输出（按注释/文档承诺）、根因分析。

## 1. 通配符 `*` 只匹配「合法标识符名」的子键，其余键静默漏配

- **文档承诺**：`pattern.go` 顶部注释与 `matchesDirect` 注释均称 `"addr.*"` "matches direct
  children of addr only"，未提及对子键名的任何限制；语义描述同样只说「单级通配符匹配直接子级」。
- **复现输入**：
  ```go
  rs, _ := Compile(Config{DefaultAllow: true, Deny: []string{"addr.*"}})
  rs.Project(map[string]any{"addr": map[string]any{
      "street":     1, // 合法标识符
      "first name": 2, // 含空格
      "café":       3, // 非 ASCII
      "a.b":        4, // 含点号
      "姓名":        5, // CJK
  }}, nil)
  ```
- **实际输出**：只有 `street` 被 `"addr.*"` 隐藏；`first name`、`café`、`a.b`、`姓名`
  全部静默落回 default（此处 DefaultAllow，故全部可见）。allow 方向镜像成立：
  `Config{Allow: []string{"addr.*"}}`（默认 deny）下这四个键被静默隐藏，尽管规则
  字面上「允许 addr 的所有直接子级」。
- **应当输出**（按文档字面语义）：`addr` 的全部直接子键都受 `"addr.*"` 支配——deny
  下全部隐藏、allow 下全部可见；或者文档明确声明「通配符只匹配
  `[a-zA-Z0-9_-]` 组成的子键名」。
- **根因**：`pattern.go` `matchesDirect` 的通配符分支最后一行
  `return validName(path[len(path)-1])`，把「模式段的字符校验」（`validatePattern`
  对规则文本的合法要求）错误地复用到了「数据键的匹配条件」上。`Project` 遍历的是
  `map[string]any` 的真实键，可以是任意字符串；规则侧的字符白名单不应约束数据侧。
  由此 deny 通配符对非常规键名失去保护力（安全敏感场景下是静默泄密面），且无任何
  报错或日志提示漏配。
- **钉住测试**：`TestWildcardDenyOnlyMatchesValidChildNames`、
  `TestWildcardAllowOnlyMatchesValidChildNames`、`TestExplainWildcardNameRestriction`。

## 2. 精确 deny 沿祖先级联，通配 deny 不级联——差异未写入文档

- **文档承诺**：`ruleset.go` `decision` 的优先级注释只写「an exact deny rule on an
  ancestor (cascades to all descendants)」；`pattern.go` 注释说通配符 "never crosses a
  segment boundary"。两处分别陈述，但没有任何文档点明这对组合的后果：**deny `"addr"`
  与 deny `"addr.*"` 对深层后代的保护力完全不同**。
- **复现输入**（对字段 `addr.geo.lat` 调 `Explain`）：
  ```go
  Compile(Config{DefaultAllow: true, Deny: []string{"addr"}})    // 精确 deny
  Compile(Config{DefaultAllow: true, Deny: []string{"addr.*"}})  // 通配 deny
  ```
- **实际输出**：
  - 精确 deny `"addr"`：`addr.geo.lat` 隐藏，`Reason=ReasonAncestorOverride`，
    `OverriddenBy="addr"`——级联生效。
  - 通配 deny `"addr.*"`：`addr.geo.lat` **可见**，`Reason=ReasonDefault`，
    `RuleRaw=""`——不级联，静默落回 default。通配 deny 只隐藏 `addr` 的直接子级
    （`addr.geo` 这个中间对象本身被 deny 命中，但 `walkMap` 为保活深层可见后代会
    让容器存活），`addr.geo.lat` 需要再写一条 `"addr.geo.*"` 才能盖住。
- **应当输出**（按「deny 一个对象应保护其内容」的直觉）：通配 deny 要么级联到所有
  后代，要么文档显式警告「`"addr.*"` 不等于隐藏 addr 子树，深层后代需逐级声明」。
- **根因**：`Compile` 中 `exactDeny` 索引只收录 `!p.wildcard` 的精确 deny
  （`ruleset.go`：`if !p.wildcard { rs.exactDeny[...] = ... }`），`ancestorDeny`
  只查这张表，通配 deny 天然被排除在级联机制之外。这是设计选择而非笔误，但属于
  安全语义的非对称，文档与注释均未声明，调用方极易误以为 `"addr.*"` 能保护整个
  addr 子树。
- **钉住测试**：`TestExactDenyCascadesButWildcardDenyDoesNot`（含 `"addr.geo.*"`
  才能直接命中 `lat` 的对照用例）。

## 3. `specificity` 只有两档且无次级排序，平级裁决不可观测

- **文档承诺**：`ruleset.go` `directMatch` 注释称 "The most specific matching pattern
  wins; at equal specificity deny wins"——承诺了平级裁决的存在，但 `Explain` 的
  `Decision` 结构没有任何字段报告「发生了平级/竞争裁决、谁输了」。
- **复现输入**：
  ```go
  // (a) 构造两条不同通配模式平级命中同一字段：
  Compile(Config{Allow: []string{"addr.*"}, Deny: []string{"addr.*"}})
  // (b) 可到达的最近竞争（exact vs wildcard）：
  rs, _ := Compile(Config{Allow: []string{"addr.*"}, Deny: []string{"addr.geo"}})
  rs.Explain("addr.geo")
  ```
- **实际输出**：
  - (a) 直接无法构造：`Compile` 报 configuration conflict。进一步地，两条**不同文本**
    的通配模式在结构上不可能命中同一字段——通配符的前缀段必须逐段等于字段的父路径，
    前缀不同则命中集合不相交。因此「两条不同通配模式同字段平级命中」在当前实现下
    不可达，「deny 胜」的平级分支对通配符而言是死代码路径。
  - (b) 返回 `Visible=false, Reason=ReasonDirectRule, RuleRaw="addr.geo",
    OverriddenBy=""`：只报告胜者，落选的 `"addr.*"` allow 无任何痕迹；反向
    （exact allow 胜 wildcard deny）同样静默。
- **应当输出**：`Explain` 应能区分「只有一条规则命中」与「多条规则竞争后裁决」，
  至少报告落选规则或竞争标志；`specificity` 若只有两档，文档应说明不存在
  深度/长度次级排序及其含义（例如 `"a.*"` 与 `"a.b.*"` 对各自层级同权）。
- **根因**：`pattern.go` `specificity()` 只有精确=2、通配=1 两档；
  `directMatch` 用 `>` 比较取每侧最优、平级 `bestDenySpec >= bestAllowSpec`
  让 deny 胜，整个过程不记录候选集；`Decision` 结构（`schema.go`）只有
  `RuleRaw`/`OverriddenBy` 两个溯源字段，前者只填胜者，后者只用于祖先级联，
  平级裁决在可观测性上是黑洞。
- **钉住测试**：`TestWildcardTieAdjudication`（三个子用例：同文本通配两侧冲突、
  不同通配无法同字段竞争、exact-vs-wildcard 裁决无元数据）。

## 备注

- 以上均为「加测试钉住现状」，未改动 `pattern.go`/`ruleset.go`/`project.go`/
  `copy.go`/`schema.go` 任何行为。
- 修复方向（不在本次范围）：去掉 `matchesDirect` 对数据键的 `validName` 限制、
  文档显式声明通配 deny 不级联（或引入级联通配语义）、`Decision` 增加竞争裁决
  的可观测字段。
