# 字段投影通配符边界：实现行为与文档承诺不一致清单

本文记录 `projection` 包（module `ontology`）中已被 characterization 测试
（`wildcard_boundary_test.go`）钉住的三处真实行为。测试断言的是**当前实际
输出**，不是期望输出；修复实现时这些测试需要同步改写。

## 1. 通配符只匹配 `validName` 子键，任意真实键名静默落 default

- 位置：`pattern.go` 的 `matchesDirect` 通配符分支末尾
  `return validName(path[len(path)-1])`。
- 文档/注释承诺：`pattern.go` 顶部与 `matchesDirect` 注释只说
  `"addr.*" matches direct children of addr only`，未声明对子键字符集的
  限制；`Project` 遍历的是 `map[string]any` 的真实键，可以是任意字符串。

复现输入（default-deny + `Allow: ["addr.*"]`）：

```go
rs, _ := Compile(Config{Allow: []string{"addr.*"}})
obj := map[string]any{"addr": map[string]any{
    "street": 1, "first name": 1, "café": 1, "a.b": 1, "姓名": 1,
}}
out, _ := rs.Project(obj, nil)
rs.Explain("addr.first name")
```

- 实际输出：`street` 可见（`ReasonDirectRule`, rule `"addr.*"`）；
  `"first name"`、`"café"`、`"a.b"`、`"姓名"` 全部**不可见**，
  `Reason=ReasonDefault`、`RuleRaw=""`，投影结果 `addr` 中只剩 `street`。
  反向配置（`DefaultAllow: true, Deny: ["addr.*"]`）下，非法名键则被
  静默**保留**（同样落 default allow），通配 deny 对它们完全不生效。
- 应当输出：文档若坚持"匹配所有直接子级"，则这些键应与 `street` 同等
  受 `"addr.*"` 管辖；若坚持名字限制，则 `Compile`/文档必须明确说明
  `"addr.*"` 只管 `[a-zA-Z0-9_-]` 键名，且对受限外的键给出可观测信号，
  而不是静默落回 default。
- 根因：`Compile` 的 `validName` 校验只作用于**规则文本**（用户写不出
  非法模式），但 `matchesDirect` 把同一判定错误地复用到**数据键名**上；
  数据侧没有任何等价约束，两层关注点被混在一个谓词里。

## 2. 通配 deny 不进 `exactDeny` 索引，不向深层级联

- 位置：`ruleset.go` 的 `Compile`：
  `if !p.wildcard { rs.exactDeny[...] = ... }`；`ancestorDeny` 只查该索引。
- 文档/注释承诺：`matchesDirect` 注释确实写了 wildcard 不跨段，但包级
  语义叙述把 deny 描述为沿祖先级联，未点明**级联只对精确 deny 成立**；
  `Decision.ReasonAncestorOverride` 的注释也只说 "an exact deny rule on
  an ancestor"，读者难以从主路径得知 `"addr.*"` 与 `"addr"` 在深层字段
  上的效果相反。

复现输入：

```go
rs1, _ := Compile(Config{DefaultAllow: true, Deny: []string{"addr"}})
rs2, _ := Compile(Config{DefaultAllow: true, Deny: []string{"addr.*"}})
rs1.Explain("addr.geo.lat")
rs2.Explain("addr.geo.lat")
```

- 实际输出：
  - `Deny: ["addr"]`：`addr.geo.lat` 隐藏，
    `Reason=ReasonAncestorOverride`、`OverriddenBy="addr"`；投影中
    `addr` 整个消失。
  - `Deny: ["addr.*"]`：`addr.geo.lat` **可见**，
    `Reason=ReasonDefault`、`RuleRaw=""`、`OverriddenBy=""`；投影中
    `addr.geo`（含 `lat`/`lng`）原样保留，只有 `addr.city` 等直接子级
       叶子被隐藏。
- 应当输出：两种配置对深层后代的级联策略应在文档中明确区分；若语义上
  期望"deny 整棵子树"，则通配 deny 也应级联（或引入显式的后代拒绝
  机制），当前是半级联的不一致状态。
- 根因：`exactDeny` 索引在 `Compile` 中用 `!p.wildcard` 过滤掉了通配
  规则，而 `decision` 的最高优先级祖先覆盖完全依赖该索引；通配 deny
  只能在 `directMatch` 中按精确深度命中，深度不符即消失，default 决定
  最终可见性——deny 与 allow 默认值的组合使结果随配置方向翻转。

## 3. `specificity` 只有两档，平级裁决不可观测，且实际无平级对手

- 位置：`pattern.go` 的 `specificity()`（wildcard=1 / exact=2）；
  `ruleset.go` 的 `directMatch`（同特异性 `>=` 时 deny 胜，同侧取遍历到
  的第一条）；`errors.go` 的 `Decision` 结构体没有任何"发生平级裁决"
  的字段。
- 文档/注释承诺：`directMatch` 注释写 "at equal specificity deny wins"，
  但未说明没有按模式深度/长度的次级排序，`Explain` 文档也未说明平级
  裁决不会被报告。

复现输入：

```go
// (a) 同侧重复通配
rs, _ := Compile(Config{Allow: []string{"addr.*", "addr.*"}})
rs.Explain("addr.city")
// (b) 跨侧同文通配
_, err := Compile(Config{Allow: []string{"addr.*"}, Deny: []string{"addr.*"}})
// (c) 任意两条不同文本的通配，如 "a.*" 与 "a.b.*"
```

- 实际输出：
  - 结构事实：两条**不同文本**的通配模式不可能同时命中同一字段
    （通配匹配固定了全部前缀段与总深度），`matchesDirect` 成对验证均
    为一真一假，因此不存在真正的"两条不同通配平级命中"。
  - 同侧重复 `"addr.*"` 可编译且两条都存入 `rs.allow`；`Explain` 只
       报告第一条（`RuleRaw="addr.*"`，两条文本相同也无从区分），无
    任何 tie 信号。
  - 跨侧相同文本在 `Compile` 阶段直接报
       `"configuration conflict: ... appears in both allow and deny"`，
    根本到不了 "deny wins at equal specificity" 分支。
- 应当输出：`specificity` 的分档与 tie-break 规则应与可达情形对齐并写清
  楚：要么补充深度/长度次级排序并在 `Decision` 中暴露裁决痕迹，要么
  删掉/收窄不可达的 "deny wins" 叙述，改为说明唯一可达的平级情形
  （同侧重复：静默首条胜；跨侧重复：编译冲突）。
- 根因：优先级机制是按"可能存在多条同特异性规则"设计的，但模式语法
  （单级、仅末尾通配、键名字符集受限）在数学上排除了不同文本通配共
  命中同一字段的可能；`directMatch` 里的同特异性比较实际只服务于
  exact-vs-exact（而 exact 同文本跨侧又被 Compile 冲突拦截），设计与
  可达输入空间不匹配，`Decision` 也缺少可观测字段。
