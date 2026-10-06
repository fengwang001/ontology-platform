# 级联删除控制器 API

## 类型

- `Policy`：`Background`、`Foreground`、`Orphan`。
- `OwnerRef{OwnerID, BlockDeletion}`：指向属主的引用，`BlockDeletion=true` 时阻止前台属主移除。
- `CreateObjectInput{ID, Owners, Finalizers}`：创建对象。ID 必须非空且全局唯一。
- `ObjectState`：对象快照，包含属主、终结器、删除标记、策略和请求时刻。
- `OperationResult`：包含本次移除对象、是否策略升级、是否无状态变化和访问统计。
- `ControllerError`：可通过 `Kind` 判断 `InvalidArgument`、`ObjectNotFound`、`Conflict`、`CycleDetected`、`OwnerMissing`。

## 操作

```go
c := cascade.NewController()

err := c.Create(cascade.CreateObjectInput{
    ID:     "child",
    Owners: []cascade.OwnerRef{{OwnerID: "parent", BlockDeletion: true}},
})

result, err := c.Delete("parent", cascade.Foreground, 10)
result, err = c.RemoveFinalizer("child", "cleanup", 11)
result, err = c.ReplaceOwners("child", []cascade.OwnerRef{{OwnerID: "new-parent"}}, 12)
err = c.AddFinalizer("child", "cleanup")
states := c.Snapshot()
```

## 语义摘要

- 重复删除成功且不改变状态；只有后台升级为前台会修改策略并重新收敛。
- 后台/前台属主移除会摘除所有依赖边；依赖者因此没有任何属主时进入后台删除。
- 孤立策略只摘边，不连带删除无属主依赖者。
- 前台删除会先传播给所有其他属主均不存在或删除中的依赖者，传播不受阻塞标志影响。
- 前台对象实际移除前，仍必须等待所有指向它的阻塞依赖者消失；非阻塞依赖者不阻止移除。
- 删除中的对象不能追加终结器或替换属主；同名终结器重复追加为无操作。
- `ReplaceOwners` 可把对象替换为无属主；这表示已经存在的对象独立存活，不自动删除。

## 测试

确定性测试覆盖三种策略、多属主、阻塞、终结器、重复删除、策略升级、环、错误优先级和并发。

随机差分测试构造 120 个固定种子，每个种子执行 90 个随机操作，并逐步比较：

- 错误类别；
- 完整对象图快照；
- 输入、实际输出和判定依据日志。

局部性测试比较 50 与 5000 个无关对象下同一删除操作的访问统计，确保成本只随实际影响闭包变化。
