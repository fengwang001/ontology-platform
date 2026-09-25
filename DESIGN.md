# DESIGN — Ontology 类型继承与接口多态

## 模型

- `Prop{Name, Type}`：属性 = 名字 + 类型名；类型名指向已注册对象类型。
- 对象类型：`name -> {parents[], ownProps[]}`，父类型必须先注册（注册序即拓扑可行序）。
- 子类型关系 `U ≼ T`：`U == T`，或沿 parent 边从 `U` 可达 `T`。
- 环检测：添加 `C` 时若 `name` 出现在其任一祖先链上即为环；父必须已存在，唯一可构造的环是自环
  （C 直接/间接以 C 为父），错误给出环上完整类型序列。

## 推导点 1：属性覆盖的方向

- 口径 A（允许相同或更宽）：子类型把父属性重定义为更宽类型。后果：把子实例当父用的消费者按
  父类型读取属性时，值的类型不再受承诺约束，里氏可替换性被破坏（看似"更安全"，实为协变反向）。
- 口径 B（要求相同或更窄）：子属性类型必须是父属性类型的子类型。后果：任何把 S 当父类型使用的
  场景，读到的仍是父承诺类型（或其子类型），可替换性成立。
- **最终选择 B**：覆盖合法但必须收窄（`childType ≼ parentType`），反向覆盖在 `AddType` 时直接报错。

## 推导点 2：多继承菱形冲突

C 继承 A、B，二者各有同名属性 p，C 自身没有 p。

- 口径 A（一律报错，要求显式覆盖）：后果：A、B 给出完全相同类型时仍强迫用户多写一行，
  把无冲突误报为冲突。
- 口径 B（静默取一边）：后果：取哪边依赖遍历/注册顺序，非确定且掩盖真正不兼容。
- **最终选择三态规则**：
  1. 两边类型相同 → 合并为一个属性（合法菱形）；
  2. 一边是另一边的子类型 → 取更窄类型（两条承诺的交集，任一处都可安全使用）；
  3. 互不兼容（互不为子类型）→ 报错，错误中列出冲突属性名与两边各自类型及来源。
  自身属性相对各继承属性另按推导点 1 校验收窄；自身与多方继承冲突时自身必须能与"合并结果"兼容。

## 推导点 3：接口契约协变方向

接口 I 要求 `p: T`，实现 S 提供 `p: U`。

- 口径 A（逆变：`T ≼ U` 即 U 更抽象即满足）：后果：消费者按 T 使用 p，实际拿到更抽象的 U，
  无法使用 T 的能力，契约形同虚设；不满足者被误判满足。
- 口径 B（协变：`U ≼ T`）：后果：消费者按 T 使用，实际拿到更具体的 U（U 处处可当 T），安全。
- **最终选择 B 协变**：`Check` 逐属性验证 `U ≼ T`。

## 包设计

### typetree

- `Tree`：`AddType(name string, parents []string, ownProps []Prop) error`；
  `Type(name) (Type, bool)`、`IsSubtype(sub, sup string) (bool, error)`、`Names() []string`。
- 注册校验顺序：空名/重名 → 父存在 → 自环及环路径（DFS 给出完整序列）→ 父属性继承冲突
  （合并各父的有效属性，套用菱形三态 + 覆盖收窄）。

### props

- `Resolve(t Tree, name string) ([]Prop, error)`：自身 + 全部祖先有效属性。
- 合并：按属性名归并各来源；多方同名走三态；自身相对继承结果必须收窄，否则报错。
- 输出按属性名排序，保证与注册/父声明顺序无关的确定性。

### iface

- `Interface{Name, Required []Prop}`；`AddInterface(name, required)` 校验空名/重名/属性唯一。
- `Check(t Tree, typeName, ifaceName) error`：
  缺属性 → `ErrMissingProp`（错误包含具体属性名，`errors.Is` 可区分）；
  类型不兼容 → `ErrPropTypeMismatch`；满足 → nil。缺失优先于类型错配报告。

### api

- `API` 为唯一入口，内部组装三包；转发 `AddType/AddInterface/Resolve/Check/IsSubtype`，
  参数做非空与存在性校验；同一底层 `Tree` 保证三处判定共享同一子类型关系。

## 错误

`ErrDuplicateType`、`ErrParentNotFound`、`ErrTypeNotFound`、`ErrCycle`（含环序列）、
`ErrPropConflict`（含两边来源与类型）、`ErrBadOverride`、`ErrDuplicateInterface`、
`ErrInterfaceNotFound`、`ErrMissingProp`、`ErrPropTypeMismatch`。

## 不变量

1. 确定性：属性按名排序；合并仅依赖类型关系，不依赖注册顺序（同一类型集合任意注册序结果逐字节相同）。
2. DAG 无环：直接/间接环都拒绝，错误指出环上类型序列。
3. 契约可区分：缺属性与类型不兼容分别用独立哨兵错误，缺属性指明属性名。
