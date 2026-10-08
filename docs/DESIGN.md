# 继承权限与乐观并发写入协调器 — 设计说明

## 总览

`ontology` 包实现一个协调器（`Coordinator`），负责两件事：

1. **继承权限判定**：对象类型构成单父继承树，权限规则声明在「类型 × 属性」上，
   子类型默认继承、可显式覆盖，覆盖沿子树向下生效并屏蔽更上层规则。
2. **乐观并发写入**：实例携带单调递增版本号，写入须携带期望版本号，
   按固定优先级判定，任何拒绝都不产生副作用。

## 数据模型

- `ObjectType`：`ID`、`Parent`（空串为根）、`Rules map[prop]Rule`（仅显式声明）、`Required []string`。
- `Rule`：`AllowRoles []string`，空为拒绝所有人，`"*"` 为放行所有人。
- `Instance`：`ID`、`Type`、`Version`（创建时为 1）、`Props map[string]any`。

关键取舍：**只存显式声明，不物化继承结果**。父类型规则变更因此天然对所有未覆盖的
子类型实时生效，无需任何传播/失效逻辑；已覆盖的子类型因为查找在其层级即命中，
天然免疫父类型变更。继承关系重组（`Reparent`）只是改一个父指针，未覆盖属性立即
按新父链解析，已覆盖属性不受影响。

## 继承链解析

`resolveRule(typeID, prop)` 从实例的具体类型出发，只沿 `Parent` 指针向根行走，
命中第一个显式声明即返回；走到根仍无声明则返回平台默认规则（拒绝写）。

- 访问的类型数恰等于链深，**与类型总量、兄弟分支数量无关**。
  由 `TestResolveVisitsOnlyChainTypes` 可验证地证明：在链深 40 不变、
  兄弟类型从 0 增至 5000 的两棵树上，访问序列长度恒为 41 且不含任何兄弟类型。
- 解析只返回最终规则与命中类型；沿途经过的中间层级不对外暴露
  （`visited` 序列仅供包内测试断言使用）。

## 写入判定顺序

`Write(objectID, expectedVersion, props, role)` 严格按以下顺序短路：

1. `ErrObjectNotFound` — 对象不存在；
2. `ErrVersionConflict` — 期望版本 ≠ 当前版本（**先于一切权限判定**，
   即使权限必然不足也先报版本冲突，且版本校验绝不会被跳过）；
3. `ErrPermissionDenied` — 对写入涉及的每个属性沿继承链解析规则，任一拒绝即失败；
4. `ErrMissingRequired` — 合并后的属性集不满足类型的必需属性列表；
5. 全部通过：合并属性、版本号严格 +1。

错误类型 `*Error` 携带可区分的 `Kind`，版本冲突与权限错误绝不混淆。
所有被拒绝的写入在加锁状态下只做读取，不改变任何版本号、规则或对象状态。

## 并发与串行等价

全部可变状态（类型表、对象表）由**一把互斥锁**保护，每个公开操作
（`CreateType/SetRule/ClearRule/SetRequired/Reparent/CreateObject/Write`）
在锁内完成「校验 + 判定 + 生效」全过程。因此：

- 任意并发交错都等价于锁的获取顺序这一全局串行顺序（线性一致）；
- 同一实例的成功写入版本号构成连续递增序列 2,3,4,…，
  该序列本身就是串行顺序的完整还原（`TestConcurrentWritesVersionSequence`）；
- 规则变更 / 重组与写入的交错同样落在同一全局串行顺序上
  （`TestConcurrentRuleChangesAndWrites`，`-race` 下验证）。

## 被放弃的方案

- **物化有效规则 + 变更传播**：父类型改规则时向下游子类型推送失效。
  放弃原因：传播本身需要与写入做复杂的并发协调，且「覆盖免疫」边界容易出错；
  只存显式声明后这些问题整体消失，代价是每次写入走一遍链（O(链深)，可接受）。
- **每实例锁 + 规则读写锁的细粒度方案**：吞吐更高，但「读规则 → 校验版本 →
  生效写入」跨两把锁，规则变更与写入之间会出现无法串行化的窗口；
  在正确性优先的目标下放弃，保留为未来优化方向（需引入版本化规则快照）。
- **默认放行（default-allow）**：对未声明任何规则的属性默认允许写。
  放弃原因：权限系统默认拒绝更安全；调用方显式声明规则的成本很低。
- **写路径上先查权限再查版本**：可减少无效版本冲突报错，但会泄露
  「该对象存在且权限不足」的信息并违反规定的判定顺序，放弃。

## 判定日志

每次判定（写入、创建、规则变更、重组）都记录 `Decision{Op, Input, Output, Basis}`：
输入、输出与依据。日志本身线程安全，测试中以 `t.Log` 打印（`go test -v` 可见）。

## 本地验证方法

```bash
export PATH=$PATH:/usr/local/go/bin   # 如 go 不在 PATH

# 全量测试（含竞态检测）
go test -race ./...

# 查看判定日志输出
go test -race -v ./ontology/

# 只跑随机对照测试（朴素模型 vs 协调器，4000 步随机操作序列）
go test -race -run TestRandomizedAgainstNaiveModel -v ./ontology/

# 只跑性能可验证性测试（链查找与类型总量无关）
go test -run TestResolveVisitsOnlyChainTypes -v ./ontology/

# 静态检查
gofmt -l .
go vet ./...
```

## 测试覆盖清单

| 需求 | 测试 |
| --- | --- |
| 覆盖后对父类型变更免疫 | `TestOverrideImmuneToParentChange` |
| 未覆盖子类型实时继承 | `TestLiveInheritanceRealtime` |
| 重组后规则重新生效 / 覆盖属性不受重组影响 / 成环拒绝 | `TestReparentReResolves` |
| 版本冲突与权限不足同时成立时的判定顺序 | `TestVersionCheckedBeforePermission` |
| 拒绝优先级全序 | `TestRejectionPriorityOrder` |
| 拒绝无副作用 | `TestRejectedWriteNoSideEffects` |
| 并发写入版本序列还原串行顺序 | `TestConcurrentWritesVersionSequence` |
| 规则变更/重组与写入并发交错可串行化 | `TestConcurrentRuleChangesAndWrites` |
| 链查找与规模无关的可验证证明 | `TestResolveVisitsOnlyChainTypes` |
| 朴素模型随机对照 | `TestRandomizedAgainstNaiveModel` |
