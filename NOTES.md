# 双写对账 NOTES

## 第三节推导（记法：`值@版本`，`T@n`=墓碑，`-`=从未写过；每行只列该写触及的键）

| # | 写操作 | A 侧 | B 侧 | 分歧？ |
|---|---|---|---|---|
| 1 | Put(a,a1,1) | a1@1 | a1@1 | 否 |
| 2 | Put(b,b1,2) | b1@2 | b1@2 | 否 |
| 3 | Put(c,c1,3) | c1@3 | c1@3 | 否 |
| 4 | PutOne(A,a,a2,4) | a2@4 | a1@1 | 是 |
| 5 | DelOne(B,c,5) | c1@3 | T@5 | 是 |
| 6 | DelOne(A,b,6) | T@6 | b1@2 | 是 |
| 7 | PutOne(A,d,d1,7) | d1@7 | - | 是 |

七写后快照：A={a:a2@4, b:T@6, c:c1@3, d:d1@7}；B={a:a1@1, b:b1@2, c:T@5, d:无}。
Reconcile 逐键：a 取 A(4>1)→a2@4；b 取 A 墓碑(6>2)→两侧墓碑@6；c 取 B 墓碑(5>3)→两侧墓碑@5；d 取 A(7>0)→d1@7。
对账后 View：a=a2, d=d1；b、c 消失。

- (甲) 忽略版本恒以 A 为准：c 错成活值 `c1@3` 残留在 View 里；正确是 B 侧墓碑@5 胜出，c 从 View 消失。
- (乙) 墓碑胜者被跳过：b 在 B 侧残留活值 `b1@2`（View 错现 b=b1）；正确是删除传播到 B，两侧均为墓碑@6，b 消失。
- (丙) 「从未写过」误判成「已删除」：d 会被当成 B 侧已删而错删，View 丢失 d=d1。区别：从未写过=版本 0，比任何真实版本小，对账时必输给对方；墓碑带真实版本，可能胜出并把删除传播到对侧——前者不产生效果，后者是有效的删除事实。

## 四条不变量落点

1. 与批量重算一致：`dwr.Reconcile` 对每分歧键取 Ver 大者写回两侧（dwr/dwr.go），等价于逐键取最大 Ver；钉于 `TestViewMatchesBatchModel`（api_test.go）。
2. 最终一致：`Reconcile` 把胜者记录整体写回 A、B 两侧；钉于 `TestReplicasConverge`（dwr_test.go）。
3. 幂等：修平后键移出脏集合，二次对账扫描 0 键、无写入；钉于 `TestReconcileIdempotent`（dwr_test.go）。
4. 失败不留痕：Key/Ver/Val 校验全部先于任何状态修改（dwr/dwr.go write 前置校验）；钉于 `TestRejectedWritesLeaveNoTrace`（api_test.go）。
