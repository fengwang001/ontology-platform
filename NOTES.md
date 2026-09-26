# 符号表作用域与遮蔽 — 推导与不变量

第三节八步（栈按 底→顶 书写）：

| 步 | 操作 | 操作后栈 | 结果 |
|---|---|---|---|
| 1 | Declare x:int | [{x:int}] | ok |
| 2 | Enter | [{x:int}] → [{}] | ok |
| 3 | Declare y:bool | [{x:int}] → [{y:bool}] | ok |
| 4 | Declare x:bool | [{x:int}] → [{y:bool,x:bool}] | ok（遮蔽，不动外层） |
| 5 | Lookup x | 同上 | bool |
| 6 | Exit | [{x:int}] | ok（内层整体丢弃） |
| 7 | Lookup x | [{x:int}] | int |
| 8 | Lookup y | [{x:int}] | ErrUndeclared |

- (甲) 第8步正确结果是 **ErrUndeclared**（y 随内层一起丢弃）。若 Exit 不弹栈或把内层绑定并入外层，第8步会错误返回 **bool**。
- (乙) 第4步正确结果是**声明成功、无错误**（跨作用域允许同名）。若禁止遮蔽，第4步报重复声明错且 x 仍为 int；若覆盖外层 x，第7步 Lookup x 会错成 **bool**（应为 int）。
- (丙) 同作用域第二次 Declare z:bool 必须报 **ErrDuplicate**，z 保持 **int**；若允许覆盖，Lookup z 会错得 **bool**。

不变量落位（保证位置 / 钉住测试）：

1. 与朴素参照一致：`scope.Stack.Lookup` 从栈顶向底逐层 map 命中即返回（scope/scope.go）→ `TestNaiveReference`
2. 遮蔽不覆盖：`Declare` 只写栈顶作用域，`Exit` 整层丢弃（scope/scope.go）→ `TestShadowRestore`
3. 重复声明拒绝：写前先查栈顶，命中即返回 ErrDuplicate 不 Put → `TestDuplicateKeepsFirst`
4. 失败不留痕：Exit/Declare/Lookup 均先校验后改状态，拒绝路径零写入；并以 `sync.RWMutex` 保证校验-写入原子（scope/scope.go）→ `TestRejectedOpsLeaveNoTrace`
