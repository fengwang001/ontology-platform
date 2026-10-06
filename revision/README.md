# revision — 修订表达式解析与消歧服务

把用户给出的修订文本解析为对象库中的唯一对象，并给出可区分的裁决。

## 识别的表达式

- 完整对象标识或唯一前缀缩写（长度受 `Config.MinAbbrev` 下限约束）
- 引用短名：全名精确命中优先；否则按配置的命名空间次序补全
- 相对导航段（可多段串联，逐段作用）：
  - `^`、`^N`：第 N 父（缺省第一父）
  - `~`、`~N`：沿第一父向上 N 代
  - `@{N}`：该引用 reflog 中第 N 次更早的值（`0` 为当前值）
  - `^{}`：剥标签直至非标签对象（对非标签无副作用成功）
  - `^{tree}`：取提交的树

## 裁决错误（按优先次序，每次只报最靠前的一个）

`ErrInvalid` → `ErrSymbolicCycle` → `ErrRefIDAmbiguous` → `ErrPrefixAmbiguous`
→ `ErrNotFound` → `ErrReflogOnID` → `ErrReflogMissing` → `ErrNotNavigable`
→ `ErrParentMissing` → `ErrBeyondHistory` → `ErrTypeMismatch`

## 用法

```go
store := revision.NewStore(
    revision.Config{MinAbbrev: 4},
    []string{"refs/tags/", "refs/heads/"}, // 短名补全命名空间次序
)
_ = store.AddObject(revision.Object{ID: id, Type: revision.TypeTree})
_ = store.SetRef("refs/heads/main", commitID)

res, err := store.Resolve("main~2^{tree}", revision.ResolveOption{
    Require: true, WantType: revision.TypeTree, AutoPeel: true,
})
abbrev, ok := store.ShortestAbbrev(commitID)
```

符号引用通过 `SetSymbolicRef(name, target)` 设置；成环（含自指）被整体拒绝，
既有引用与 reflog 不变。所有写操作与解析可任意并发，解析看到的是某个瞬间的完整状态。

## 文档与测试

- 设计取舍与放弃方案见 `DESIGN.md`
- 测试：`go test -race -v ./...`（每个用例打印输入、实际输出与判定依据）
  - 12 组覆盖面用例：消歧规则、各导航段合法/非法、首败段定位、符号引用、类型约束等
  - `TestRandomDifferential`：随机对象库/表达式对独立朴素模型的差分
  - `TestConcurrentSerializability`：并发写读的串行等价可观测校验
  - `TestScalingSublinear`：前缀匹配与缩写不随对象总数线性增长
  - `TestHexTrieDirect`：基数树压缩边/计数/缩写的直接单测
