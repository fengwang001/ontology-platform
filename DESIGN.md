# Ontology 统一错误类型体系设计

目标：所有失败归入带类型错误（NotFound / AlreadyExists / Conflict / Validation /
PermissionDenied / Internal），支持 `errors.Is`/`errors.As` 沿类型层级与 cause 链匹配、
批量错误聚合（可索引、可遍历）、HTTP 映射与跨进程序列化 round-trip 保真。

## 总体结构

- `errdef`：`Code` 枚举、`Base`（code+message+对象/属性上下文+cause）与各具体类型，
  具体类型内嵌 `*Base`；类型层级通过自定义 `Is` 表达（具体类型 Is 其基类哨兵）。
- `wrap`：`Causer` 包装器，只追加对象/属性上下文，不改类型；`Unwrap` 暴露 cause。
- `aggregate`：`Aggregate` 持有有序子错误切片，`Unwrap() []error` 暴露全部子错误。
- `map`（目录 `errmap`，避免与内建 `map` 冲突）：Code→HTTP 状态码；JSON 按
  code+message+context+cause 编码，按 code 重建类型。

## 推导点 1：包装后的 Is 匹配

问题：`Wrap(ErrNotFound, ctx)` 后 `errors.Is(wrapped, ErrNotFound)` 是否为真？

- 口径①只看最外层：包装即新类型，`Is` 失配。
  - 后果：调用方拿不到原始类型，无法按类型分流；只要中间层加过上下文，
    最底层语义就被永久遮蔽，包装层数越深越不可用。
- 口径②沿 cause 链匹配：`Is` 递归 `Unwrap`，链上任一层命中即为真。
  - 后果：包装语义被准确定义为「加上下文」而非「换类型」，底层类型始终可被分流；
    代价只是沿链的线性遍历。

最终选择：口径②。包装器实现 `Unwrap() error` 返回 cause，依赖标准库
`errors.Is` 的递归机制穿透包装；`Base.Is(target)` 先按指针/类型层级判定，
再按 code 判定，最后 `errors.Is(cause, target)`，保证类型层级与 cause 链双向可匹配。
复杂度为链长的线性函数（见 `wrap/isCalls` 计数器：100/10000 层包装，
判定次数随层数线性，且 code 命中即提前返回，不随无关包装层数爆炸）。

## 推导点 2：聚合错误的 Is 匹配

问题：`Aggregate{ErrNotFound, ErrConflict}` 上 `errors.Is(agg, ErrConflict)` 是否为真？

- 口径①只匹配容器本身：聚合是「容器」，自身不代表任何类型，`Is` 永远失配。
  - 后果：批量操作失败后，调用方无法回答「这批里有没有冲突/未找到」，
    只能手工拆解；类型分流在批处理场景整体失效。
- 口径②匹配任一子错误：任一子错误命中即为真。
  - 后果：批量失败可直接按类型分流（有 Conflict 就提示重试/改写）；
    容器本身仍可索引、可遍历，不丢任何子错误与各自上下文。

最终选择：口径②，并保持容器的结构化访问。`Aggregate` 实现
`Unwrap() []error`（Go 多错误展开约定），标准库 `errors.Is` 会对每个子错误
递归判定；另提供 `At(i)`（按序索引）、`Len()`、`Range(fn)`（按聚合顺序遍历）。
不变量：`Len()==len(子错误)`，`Range` 输出与聚合顺序逐字节一致。

## 推导点 3：序列化 round-trip 保真

问题：`Marshal`→`Unmarshal` 后 `errors.Is(e, ErrNotFound)` 是否仍为真？

- 口径①只序列化 message 字符串：反序列化得到无类型普通错误。
  - 后果：跨进程后类型信息全部丢失，`Is` 失配，接收方无法按类型处理，
    只能解析字符串，脆弱且无法演进。
- 口径②序列化 code+message+context+cause：反序列化按 code 重建具体类型。
  - 后果：类型标识、上下文与 cause 链全部保真，跨进程 `Is` 与本地一致；
    需显式处理未知/未来新增 code。

最终选择：口径②。wire 结构递归编码 `{"code","message","object","property",
"cause",...}`；反序列化时已知 code 用对应构造器重建（具体类型内嵌 `*Base`，
故仍命中类型层级），未知 code 降级为携带该 code 的通用 `Base` 错误
（`Is(code)` 仍可按 code 匹配、HTTP 降级 500），绝不 panic。
对非本体系错误用 `{"type":"plain","message":...}` 兜底，同样不丢信息。

## 类型层级与 Code 映射

```
Base（携带 Code，是所有错误的基类）
 ├─ NotFound(CodeNotFound→404)
 ├─ AlreadyExists(CodeAlreadyExists→409)
 ├─ Conflict(CodeConflict→409)
 ├─ Validation(CodeValidation→400)
 ├─ PermissionDenied(CodePermissionDenied→403)
 └─ Internal(CodeInternal→500)
```

具体错误哨兵：`errdef.ErrNotFound` 等为各类型的无因实例；
`errdef.ErrBase` 是宽基类哨兵（CodeBase=0）。任意具体错误
`errors.Is(e, errdef.ErrBase)` 为真（层级向上匹配），反之不成立。

## HTTP 映射

单错误按 code 直接映射；`Aggregate` 取所有子错误状态码：含 5xx → 500，
否则含 4xx → 400，否则 200（空聚合不构成失败）；无法识别的类型 → 500。

## 跨模块不变量

1. 传递性与线性：cause 链上 `Is` 逐层穿透；`wrap` 包非导出计数器证明
   100/10000 两档判定次数为线性且命中即止。
2. 聚合不丢子错误：子错误数 == 传入数，`At`/`Range` 保持传入顺序，
   上下文逐一带出。
3. round-trip 保真：序列化前后 `Is` 结果一致，对象/属性上下文一致，
   cause 链长度一致。

所有「遍历错误类型 × 包装层数 × 聚合形态 × 未知 code」的验证均以
表驱动 + 循环完成，不展开为多个测试函数。
