# 字段级投影：通配符边界行为与文档承诺不一致清单

下列均为**当前实现的真实行为**（已由 `wildcard_boundary_test.go` 钉住），
未修改任何实现文件。

## 1. 通配符只匹配 `validName` 子键，异常键名静默落回 default

- 位置：`pattern.go` 的 `matchesDirect` 通配分支末尾
  `return validName(path[len(path)-1])`。
- 复现输入：
  - `Compile(Config{DefaultAllow: true, Deny: []string{"addr.*"}})`
  - 对象 `{"addr": {"street":1, "first name":2, "café":3, "a.b":4, "姓名":5}}`
- 实际输出：投影后 `addr` 中只剩 `"first name"`、`"café"`、`"a.b"`、
  `"姓名"`；`street` 被隐藏。即这些键全部未被 `addr.*` 命中，走 default
  （allow）。换成 `Config{Allow: []string{"addr.*"}}`（默认 deny）时结果
  反转：只剩 `street`，四个异常键被默认拒绝。
- 应当输出（按文档承诺「matches direct children of addr」）：`addr` 的全部
  直接子键都应被 `addr.*` 命中并 deny，`addr` 因无子键存活而整体消失；
  allow 侧同理，五个子键都应可见。
- 根因：规则模式的合法字符集 `[a-zA-Z0-9_-]`（Compile 用它校验规则文本）
  被复用为「被匹配的真实 map 键」的过滤器。Project 遍历的是
  `map[string]any` 的真实键，可以是任意字符串（空格、点号、Unicode），
  两套字符域被错误地等同。文档从未声明子键名受限。
- 附带后果：`Explain("addr.a.b")` 无法表达键名 `"a.b"`——点分输入被切成
  三段，因深度不符直接落 default，调用方没有任何途径查询该键的裁决。

## 2. 通配 deny 不级联，与精确 deny 的差异未写进文档

- 位置：`ruleset.go` 的 `Compile` 中仅在 `!p.wildcard` 时写入
  `exactDeny`；`ancestorDeny` 只查该索引。
- 复现输入：
  - 精确：`Compile(Config{Allow: []string{"addr.geo.lat"}, Deny: []string{"addr"}})`
  - 通配：`Compile(Config{DefaultAllow: true, Deny: []string{"addr.*"}})`
  - 查询：`Explain("addr.geo.lat")`
- 实际输出：
  - 精确 deny：`Visible=false, Reason=ReasonAncestorOverride, RuleRaw="addr"`，
    投影中整个 `addr` 子树消失，即使存在精确 allow 也被覆盖。
  - 通配 deny：`Visible=true, Reason=ReasonDefault, RuleRaw=""`；投影中
    `addr.geo` 这个直接子键虽被隐藏，但 `addr.geo.lat` 存活，中间对象因
    `walkMap`「有存活子节点即存活」而保留，最终 lat 仍可见。
- 应当输出：若按「`addr.*` 管理 addr 的直接子级」的直觉，通配 deny 是否
  级联本身可以是设计选择，但文档/注释必须写明差异。当前 `pattern.go`
  只在 `matchesDirect` 注释中提到「never match deeper descendants」，
  `ruleset.go` 的优先级文档只写「an exact deny rule on an ancestor
  (cascades...)」，从未显式声明「通配 deny 不进祖先索引、不级联」，
  调用方无法从规则文本预期 `addr.geo.lat` 仍可见。
- 根因：级联能力由「是否进入 `exactDeny` 索引」单一开关决定，而该开关
  与「精确 vs 通配」耦合；通配规则只有逐字段 direct match 一条生效路径。

## 3. specificity 只有两档，平级裁决不可观测且无次级排序

- 位置：`pattern.go` 的 `specificity()`（wildcard=1，其余=2）；
  `ruleset.go` 的 `directMatch`（同特异性取遍历时的首个，双方都命中时
  `bestDenySpec >= bestAllowSpec` 判 deny）。
- 复现输入与实际输出：
  1. 任何两条**不同**、经 `Compile` 产生的通配模式不可能在同一字段同时
     命中：`*` 恒为末段，前缀相同即模式文本相同。已用
     `{a.*, b.*, a.b.*, x.y.*}` 对全部候选路径交叉验证，无一对同时
     `matchesDirect`。因此「不同通配模式平级裁决」在公开 API 下无法发生。
  2. 同文本通配同时存在于 allow/deny（绕过 Compile 的冲突检查构造，
     `Explain("addr.city")`）：deny 胜，返回
     `{Visible:false, Reason:ReasonDirectRule, RuleRaw:"addr.*"}`。
  3. 同前缀、同特异性的两条 allow 通配（内部构造，raw 不同）：切片中
     **第一条**胜；反转切片顺序赢家随之反转。
- 应当输出：
  - 平级时应有确定的次级排序（如模式深度/长度），而非依赖切片顺序；
  - `Decision` 应能区分「唯一命中」与「平级裁决」（例如新增 reason 或
    记录被否决规则），当前 `Explain` 输出与普通直接命中完全相同，
    调用方无法得知发生过 tie-break。
- 根因：`specificity()` 只有 1/2 两档，`directMatch` 用严格大于
  （`> bestSpec`）保留首个同分规则、用 `>=` 在 deny/allow 之间偏向 deny，
  把「声明顺序」隐式当成了次级排序，而 `Decision` 结构没有承载该信息的
  字段。`decision_test.go` 中既有注释也已注意到「two wildcard rules ...
  distinct text ... cannot match the same leaf」，但该结构性限制同样未
  写入文档。

## 备注

- 本次仅新增 `wildcard_boundary_test.go` 与本文件；既有实现与测试未改。
- 环境：`go test ./...` 需要可写缓存目录（如 `GOCACHE=/tmp/go-cache`）。
