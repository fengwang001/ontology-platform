# NOTES

## 八行分步表（栈从底到顶；i=int，b=bool）
1. Declare x:i | [{x:i}] | OK
2. Enter | [{x:i}][{}] | OK
3. Declare y:b | [{x:i}][{y:b}] | OK
4. Declare x:b | [{x:i}][{y:b,x:b}] | OK（内层遮蔽）
5. Lookup x | 栈同 4 | b（取最近绑定）
6. Exit | [{x:i}] | OK（内层绑定整体丢弃）
7. Lookup x | 栈同 6 | i（外层原样恢复）
8. Lookup y | 栈同 6 | ErrUndeclared

(甲) 第8步正确结果是「未声明」错误。若 Exit 不弹出内层（或把内层绑定并入外层），则 y:b 仍可见，第8步会错得 bool。
(乙) 第4步正确结果是 OK，仅在内层遮蔽外层。禁止遮蔽则第4步报重复声明错、内层无 x；若把外层 x 覆盖成 bool，则第7步错得 bool（应为 int）。
(丙) 同一作用域 Declare z:i 后再 Declare z:b：第二次报 ErrDuplicate 且 z 保持 int，Lookup z 得 int；允许覆盖的实现会错得 bool。

## 第二节四条不变量：代码保证位置 / 钉住的测试函数
1. 朴素参照一致：scope/scope.go 的 Lookup 自顶向下逐层 map 探测、取首个命中 / TestNaiveReference
2. 遮蔽不覆盖：Enter 压入新 map、Declare 只写当前层、Exit 整体弹栈 / TestShadowingRestored
3. 重复声明拒绝：Declare 先 Get 判重、命中即返回 ErrDuplicate 且不 Put / TestDuplicateKeepsFirst
4. 失败不留痕：Exit 先判 len>1 再弹、Declare 先判后写、Lookup 无写操作 / TestRejectedOpsLeaveNoTrace
附：非导出计数器 probes 断言 O(1) / TestProbeCountConstant；并发一致 / TestConcurrentLookup；对外自检 / api/api_test.go 的 TestSelfCheck。
