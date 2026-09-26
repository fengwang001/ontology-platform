# Ontology 统一错误类型体系设计

目标：所有失败归入带类型的错误，支持沿「类型层级 + cause 链」的
`errors.Is`/`errors.As`，支持批量聚合，并可映射 HTTP、跨进程序列化保真。

## 类型模型

- Code 枚举（errdef 包）：`CodeUnknown/NotFound/AlreadyExists/Conflict/
  Validation/PermissionDenied/Internal`。
- `Error` 结构字段：`code Code`、`msg string`、`object string`、`property string`、
  `cause error`；通过 `WithObject/WithProperty/WithCause` 选项构造。
- 类型层级：一个抽象基类 `ErrBase`（CodeUnknown，代表「本体错误」这一宽口径），
  以及每种具体 code 的哨兵实例（`ErrNotFound` 等）。`Error.Is(target)`：
  target 同为 `*Error` 时，target 是基类则恒真（具体类型 Is-A 基类），
  否则按 code 相等判定。因此
  `errors.Is(New(CodeNotFound), ErrBase)` 为真、对 `ErrNotFound` 为真、
  对 `ErrConflict` 为假——既支持按宽基类分流，也支持按具体码分流。

## 推导点 1：Wrap 之后 Is 是否穿透

- 可选口径 A（只看最外层）：包装即「换类型」，`Wrap(ErrNotFound,ctx)` 后
  `errors.Is(wrapped, ErrNotFound)` 为假。
  - 后果：任何加上下文的动作都会抹掉原始类型，调用方无法对被包装错误做类型分流，
    只能字符串匹配，等于废掉类型体系。
- 可选口径 B（沿 cause 链匹配）：包装只是「加上下文」，`wrapped.Unwrap()`
  返回内层错误，`errors.Is` 标准算法递归穿透，链上任一层命中即为真。
  - 后果：上下文与类型二者兼得；`As` 同样沿链取到具体 `*Error`。
- 最终选择：**B**。`wrap.wrapper` 持有内层 cause 并实现 `Unwrap() error`，
  自身不实现 `Is`，完全复用标准库的 cause 链递归。

## 推导点 2：Aggregate 的 Is 是否匹配任一子错误

- 可选口径 A（只匹配容器本身）：`Aggregate` 是不透明容器，`Is` 恒假。
  - 后果：调用方无法回答「这批失败里有没有冲突/未找到」，批量操作只能整体当
    未知失败处理。
- 可选口径 B（匹配任一子错误）：聚合体实现多子错误展开，任一子错误命中即为真。
  - 后果：批量失败仍可按类型分流；同时容器按序保存子错误，支持
    `Len/At(i)/Range` 取到「第几个失败、对象/属性是什么」。
- 最终选择：**B**。`Aggregate` 实现 `Unwrap() []error`（Go 多子错误协议），
  标准库 `errors.Is` 天然对每个子错误递归；另提供 `Len/At/Range` 保证可索引、
  顺序与传入逐字节一致。

## 推导点 3：序列化 round-trip 是否保留类型

- 可选口径 A（只存 message 字符串）：反序列化得到无类型普通错误。
  - 后果：跨进程后 `errors.Is(_, ErrNotFound)` 全部失效，HTTP 映射也只能 500。
- 可选口径 B（存 code+message+context+cause）：按 code 重建对应具体类型。
  - 后果：跨进程后类型层级、`Is`、上下文与 cause 链长度全部保真；
    对未知/未来新增 code，降级为「携带该 code 的通用错误」而非 panic。
- 最终选择：**B**。

## HTTP 映射与序列化格式

- HTTP：NotFound→404，AlreadyExists/Conflict→409，Validation→400，
  PermissionDenied→403，其余（含 Internal、基类、未知）→500。
  Aggregate 取首个子错误的类型映射（最先发生的失败决定响应码）。
- JSON 节点：`{"code","message","object","property","cause"}`；
  包装节点追加 `"wrap":true`；聚合节点为 `{"aggregate":true,
  "errors":[...]}`。未知 code 字符串原样保留（Error 增加 `rawCode string`），
  再序列化不丢信息。包目录名 `map` 但包名取 `errmap`，避免与内建 `map` 冲突。

## 跨模块不变量（非导出计数器证明）

- `errdef.isCompareCount` 在每次命中「具体类型比较」时 +1。包装 100 层与
  10000 层时计数器均为 1：穿透成本只取决于链上目标比较次数，不随无关包装层数爆炸。
- 聚合：`Len()==len(children)`，`At(i)` 按序，`Range` 顺序与传入一致。
- round-trip：序列化前后 `errors.Is` 结果一致，object/property 一致，
  cause 链长度一致；未知 code 反序列化返回带码通用错误、不 panic。
