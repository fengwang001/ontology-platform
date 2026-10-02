# RBAC 管理器（层级角色 + SSD/DSD 约束）

包路径：`ontology/rbac`。提供带角色继承、用户分配、会话激活与权限检查的
RBAC 管理器，支持静态职责分离（SSD）与动态职责分离（DSD）约束。

## 核心定义

- 继承边 `(senior, junior)`：持有 `senior` 即同时拥有 `junior` 的全部权限；
  边构成有向无环图（DAG）。
- `Juniors(r)`：`r` 本身加上沿继承边可达的全部 junior。
- `Auth(u)`：用户 `u` 被直接分配的全部角色各自的 `Juniors` 之并。
- `A(s)`：会话 `s` 的显式激活角色集；其中每个角色必须属于所属用户的 `Auth`。
- `Eff(s)`：`A(s)` 中各角色的 `Juniors` 之并（有效激活集）。

## 约束违反判据

- SSD 约束 `(名称, RS, n)`：任何用户 `u` 满足 `|Auth(u) ∩ RS| < n`；
  交集元素个数不小于 `n` 即违反。
- DSD 约束 `(名称, RS, n)`：任何会话 `s` 满足 `|Eff(s) ∩ RS| < n`。
- `RS` 至少含 2 个互不相同的已存在角色，`2 ≤ n ≤ |RS|`。
- SSD 与 DSD 使用各自独立的命名空间。

## 各操作的检查对象

| 操作 | 检查对象 |
| --- | --- |
| `AddInherit(senior, junior)` | 先拒绝重复边与成环（含自环）；再对所有 `senior ∈ Auth(u)` 的用户用 `Auth ∪ Juniors(junior)` 检查全部 SSD；无 SSD 违反后，再对所有 `senior ∈ Eff(s)` 的会话用 `Eff ∪ Juniors(junior)` 检查全部 DSD |
| `DeleteInherit(senior, junior)` | 边不存在则拒绝；生效后重算受影响用户的 `Auth` 并做级联停用 |
| `AssignUser(user, role)` | 拒绝重复分配；用 `Auth(user) ∪ Juniors(role)` 检查全部 SSD |
| `DeassignUser(user, role)` | 未分配则拒绝；生效后重算 `Auth(user)` 并做级联停用 |
| `AddSSD` / `AddDSD` | 名称不重复时，用现有全部用户（会话）检查新约束，已有违反者即拒绝且不登记 |
| `Activate(sid, role)` | 要求 `role ∈ Auth(owner)` 且未被显式激活（仅被蕴含者允许显式激活，Eff 不变）；用新 `Eff` 检查全部 DSD |
| `Deactivate(sid, role)` | 只能停用显式激活角色；仅被蕴含时报「隐式激活」并带出升序的蕴含者列表；根本未激活报「未激活」 |
| `Check(sid, perm)` | `perm` 属于 `Eff(sid)` 中某角色的权限集时为真 |

## 级联停用规则

`DeassignUser` 与 `DeleteInherit` 生效后，对受影响用户的会话逐一判定：
显式激活角色若不再属于新 `Auth` 则被停用（仍可经其他路径到达的角色不受
影响），随后重算会话 `Eff`。两者返回被停用的 `(会话, 角色)` 列表，按会话
编号再按角色名字节序升序。停用只会缩小 `Eff`，不会产生新的 DSD 违反。

## 拒绝次序与零副作用

每个操作只报告第一个匹配的错误类别，顺序为：

1. **参数非法**（构造参数越界、名称为空、RS 个数或 n 越界、RS 内角色重复）
2. **不存在**（角色、用户、会话；多对象按参数顺序第一个；`AddSSD`/`AddDSD`
   按 RS 给定顺序第一个不存在的角色）
3. **冲突**（重复登记、重复边、成环、重复分配、未分配、边不存在、重复显式
   激活、未授权激活、隐式激活、未激活）
4. **约束违反**（SSD 先于 DSD；报告约束名与对象名，取约束名字节序最小者，
   其下取用户/会话名字节序最小者）
5. **超限**（每用户会话数超过 `Smax`，或约束总数超过 `Cmax`）

被拒绝的操作不改变任何角色、边、分配、约束、会话与权限（全部校验通过后才
施加修改）。

## 并发与确定性

所有操作由单把互斥锁串行化：并发调用等价于某个串行顺序，约束检查与其后
的修改是一个原子步骤，`Check` 看到的是某一时刻的一致状态。任何时刻所有
用户满足全部 SSD、所有会话满足全部 DSD、所有会话的显式激活角色都属于所
属用户的 `Auth`。相同操作序列重放得到完全相同的列表与错误。

## 检查计数器

`AddInherit` 与 `DeleteInherit` 通过「角色 → 用户 / 会话」反查索引定位受
影响对象，非导出计数器记录最近一次调用检查或重算的去重用户数与去重会话数
（`LastCheckCounts()` 读取）：

- `AddInherit`：用户数为 `senior ∈ Auth(u)` 的用户数（为找出名字最小违反
  者须检查全部受影响用户）；SSD 阶段已违反时不进入 DSD 阶段，会话数为 0，
  否则为 `senior ∈ Eff(s)` 的会话数。
- `DeleteInherit`：用户数为 `senior ∈ 旧 Auth(u)` 的用户数；会话数为实际
  做停用判定的会话数（属主受影响且显式激活了 `旧 Juniors(junior)` 中角色
  的会话）。

计数只与受影响对象个数有关：系统中另有 10^4 个无关用户与会话时，同一次调
用的计数完全相同（见 `TestCountersIgnoreUnrelatedUsersAndSessions`）。

## 本地验证

```bash
# 全量测试（含 2000 组随机序列与朴素模拟器的逐步对照）
go test ./rbac/

# 查看随机对照日志（输入、输出与判定依据）
go test ./rbac/ -run TestDifferentialRandom -v

# 竞态检测
go test -race ./rbac/

# 覆盖率
go test -coverprofile=coverage.out ./rbac/
go tool cover -func=coverage.out
```
