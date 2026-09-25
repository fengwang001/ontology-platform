# Ontology 统一错误类型体系设计

## 目标

所有失败归入带类型错误（NotFound/AlreadyExists/Conflict/Validation/PermissionDenied/Internal 等），
支持类型层级上的 `errors.Is`/`errors.As`、cause 链穿透、批量错误聚合（可索引、可遍历、
Is 命中任一子错误）、HTTP 状态码映射，以及跨进程序列化 round-trip 后类型保真。

## 包结构

- `errdef`：code 枚举、`Base`（统一基类）+ 六个具体类型（嵌入 `*Base`）、构造器。
- `wrap`：`Wrap(err, ctx...)`，只加对象/属性上下文，不换类型。
- `aggregate`：`Aggregate []error` 容器，支持索引/遍历/Is 任一命中。
- `maperr`：HTTP 映射 + JSON Marshal/Unmarshal。目录不能叫 `map`（Go 关键字），包名 `maperr`。
- `cmd/demo`：逐条打印 7 项语义 OK/FAIL，退出码 0。

## 推导点 1：包装后的 Is 匹配

问题：`Wrap(ErrNotFound, ctx)` 后，`errors.Is(wrapped, ErrNotFound)` 是否应为真？

- 口径①（只看最外层）：包装产生新类型，`Is` 只比较最外层。
  后果：包装「加上下文」被语义化成「换类型」，调用方按 NotFound 分流全部失配，
  包装层数越深原始类型越不可见，调用方只能解析字符串，类型体系名存实亡。
- 口径②（沿 cause 链匹配）：外层实现 `Unwrap() error` 暴露 cause，
  `errors.Is` 递归穿透；外层自身先做一次 code 相等的短路匹配。
  后果：包装不改变错误身份，只增加上下文，任何层数的包装都能被原类型命中。
- **最终选择：口径②。** `wrapped` 复制 cause 的 code 作为自身 code，并 `Unwrap` 返回 cause；
  匹配先看自身 code，再让标准库递归 unwrap，包装=装饰而非替换。

## 推导点 2：聚合错误的 Is 匹配

问题：`Aggregate{ErrNotFound, ErrConflict}` 上 `errors.Is(agg, ErrConflict)` 是否应为真？

- 口径①（只匹配容器本身）：聚合体是容器，没有自己的类型，Is 永远 false。
  后果：批量调用方无法回答「这批里有没有冲突/有没有权限失败」，无法按类型批量分流，
  只能自己遍历并类型断言，聚合等于信息黑箱。
- 口径②（匹配任一子错误）：`Aggregate.Is(target)` 对每个子错误递归 `errors.Is`，任一命中即真；
  同时容器可按序索引 `At(i)`/`Len()`/遍历，暴露每个子错误各自的上下文。
  后果：既能整体类型分流（存在性），又能精确定位「第几个对象、因什么上下文失败」。
- **最终选择：口径②。** 另实现 `Unwrap() []error`（Go 1.20+ 多 unwrap），
  使 `errors.As`/`errors.Join` 语义同样穿透全部子错误；容器本身不伪造单一 code。

## 推导点 3：序列化 round-trip 保真

问题：Marshal→Unmarshal 后 `errors.Is(got, ErrNotFound)` 是否应为真？

- 口径①（只序列化 message）：跨进程后得到无类型普通 error。
  后果：类型标识永久丢失，远端/重试/日志重放场景 `Is` 全部失配，HTTP 码只能靠文本猜测，
  与「统一类型体系」直接矛盾。
- 口径②（序列化 code + message + context + cause 树）：
  按节点递归编码；反序列化按 code 经构造器注册表重建具体类型，再重建 cause 链与
  聚合子错误树。**未知/未来新增 code 不 panic**，降级为携带该 code 的 `GenericError`
  （其 code 可被读取、HTTP 降级 500、与同 code 目标仍可 Is）。
- **最终选择：口径②。** code 是类型身份的稳定线格式（枚举字符串），message 仅展示用；
  context（object/property）与 cause 链长度逐节点保真。

## 类型层级与匹配规则

```
error
 └─ *Base（code+message+object+property+cause）  ← 宽基类 ErrBase（code CodeBase）
     ├─ *NotFoundError        CodeNotFound
     ├─ *AlreadyExistsError   CodeAlreadyExists
     ├─ *ConflictError        CodeConflict
     ├─ *ValidationError      CodeValidation
     ├─ *PermissionDeniedError CodePermissionDenied
     └─ *InternalError        CodeInternal
```

- 具体类型嵌入 `*Base`；`Base.Is(target)`：target 实现 `CodeProvider` 时比 code，
  且宽基类 sentinel `ErrBase`（code=CodeBase）命中任意 typed error。
- `errors.As` 走标准嵌入提升：`*NotFoundError` 与 `*Base` 均可被取出。
- `Wrap` 产物与 `Aggregate` 同样实现 `CodeProvider`，全链 code 可读。

## HTTP 映射

NotFound→404，AlreadyExists→409，Conflict→409，Validation→400，
PermissionDenied→403，Internal/未知 code→500。Aggregate 返回第一个子错误的码。

## 跨模块不变量（非导出计数器证明）

- 匹配不随无关包装层数爆炸：`errdef` 与 `wrap` 各持非导出 `isSteps` 计数器，
  `Is` 每比较一次加一；100/10000 两层包装下，命中点在「自身 code 短路」处，
  比较次数为常数，不随深度增长。
- 聚合不丢子错误：`Len()==len(in)`，`At(i)` 按序，遍历与入参逐字节一致。
- round-trip 保真：对「全部 code × 包装 0/1/3 层 × 平铺/嵌套聚合 × 未知 code」
  表驱动循环，断言 Is 结果、object/property、cause 链长度序列化前后一致。
