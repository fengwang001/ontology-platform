# Ontology 校验钩子：DESIGN

模块 `ontology`（Go 1.26）。四个包：

- `snapshot`：对象状态的不可变、字节级确定性快照。
- `hook`：钩子定义/注册，`check(snapshot) → (pass, reason)`，只读。
- `schedule`：按 phase 分组、组内确定性排序，产出可复现执行序列。
- `api`：组装三包，唯一入口 `Validate(obj, change)`，哨兵错误可 `errors.Is`。

对象模型：对象 = `{type, id, props map[string]string}`；变更 = 对 props 的部分覆写。
执行模型：pre 钩子看变更前快照 → 应用变更（内存内）→ post 钩子看变更后快照；post 失败则回滚。

## 推导点 1：跨字段不变量挂 pre 还是 post

**口径 A：挂 pre（看到旧值）。**
后果：变更只改 `end`、不改 `start` 时，钩子拿旧 `start` 与新 `end` 比较。
- 旧状态已违反（旧 start > 旧 end）时，与本次变更无关也会被判失败，且无法被任何只改 end 的修复通过。
- 更严重：旧状态合法，新值 `end < 旧 start` 构成新违反时，pre 只看到旧值，**静默放过违规变更**。

**口径 B：挂 post（看到新值）。**
后果：判定对象永远是「变更提交后的完整新状态」，校验的是新状态是否满足不变量，
与本次改了哪个字段无关；旧值如何无关紧要。失败即回滚，违规状态永不落地。

**选择：跨字段不变量必须挂 post；单字段合法性（范围、格式、非空）可挂 pre。**
pre 只适合「只依赖被校验字段自身」的检查；凡引用两个及以上字段的不变量一律 post。

## 推导点 2：钩子读活对象还是冻结快照

**口径 A：钩子可变，读活对象。**
后果：后一个钩子看到前一个钩子改过的状态，结果依赖注册顺序（换序结果不同），
无法并行（存在读写竞争），且校验逻辑可借副作用篡改被校验对象，语义不可证。

**口径 B：钩子只读 + 冻结快照。**
后果：同一 phase 内所有钩子拿到同一份不可变快照（pre 共享变更前快照，post 共享变更后快照）。
无任何写入口，顺序执行与乱序执行结果逐字节一致，可安全并行；快照按属性名排序做字节级固化，
「跑前/跑后字节相同」可直接断言。

**选择：只读契约 + 冻结快照。** `check` 签名只接收 `*snapshot.Snapshot`（无 map 暴露、无写方法）；
快照内容来自排序后的确定性字节串；pre/post 跑完后重新固化，与跑前逐字节比对证明零修改。

## 推导点 3：首错即停还是失败聚合

**口径 A：首错即停。**
后果：一次只暴露一个失败，用户改完再提交再失败，往返次数等于失败钩子数。

**口径 B：聚合同阶段全部失败。**
后果：同阶段所有钩子都执行，每条失败带钩子名 + reason，一次返回全部。
约束：pre 只要有失败就**绝不运行 post**——post 校验的是变更后新值，pre 不过意味着变更被拒，
跑 post 是在一个注定不提交的状态上空转，甚至读到「半提交」中间态。

**选择：同阶段聚合、阶段间短路。** pre 全过 → 应用变更 → post 全跑；
post 任一失败 → 回滚（返回新状态快照作为现场，活对象恢复为变更前）。
另对钩子 panic 归为「钩子内部错误」，与业务失败分开。

## 包设计

`snapshot`
- `Freeze(typ, id string, props map[string]string) *Snapshot`：属性名排序，固化为确定字节串。
- `Get(name)` 只读访问；`Bytes()` 返回固化字节；相等性由字节串保证。

`hook`
- `Phase`：`PhasePre` / `PhasePost`；`Check func(*snapshot.Snapshot) (bool, string)`。
- `Hook(name, appliesTo, phase, priority, check)`；`Registry.Register` 赋递增注册序号。
- 按 `appliesTo` 建 map 索引：匹配只访问该类型桶 + 全局桶，计数器记录访问数，
  与已注册钩子总数无关（100 与 10000 两档访问数相等）。

`schedule`
- `Plan(hooks) []hook.Hook`：先按 phase 分组（pre 全部先于 post），
  组内按 priority 降序、再按注册序号升序排序；排序键不含 map 迭代序，执行序列可复现。

`api`
- `Validate(obj, change)`：参数校验（nil/未知类型/空变更）→ 规划 → pre 聚合执行
  → 应用变更 → post 聚合执行 → post 失败回滚。
- 哨兵：`ErrPreFailed`、`ErrPostFailed`、`ErrHookFault`（panic）、`ErrInvalidArgument`，
  失败明细 `[]Failure{Hook, Phase, Reason}`，可 `errors.Is`。

## 不变量证明（非导出计数器 / 字节比对）

- 顺序确定性：两次以不同注册顺序注册同一组钩子，规划出的序列逐字节相同。
- 只读契约：pre 全跑后快照字节 == 变更前固化字节；post 全跑后 == 变更后固化字节。
- 匹配效率：`lookupVisits` 仅统计被检查的索引桶内候选数；100 钩子与 10000 钩子两档
  对同一变更访问数相同，证明不随注册总数线性增长。
