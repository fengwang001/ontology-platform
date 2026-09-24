# NOTES

## 六步推导（序列按 score 降序、id 升序；三元组 = ROW_NUMBER,RANK,DENSE_RANK）
1. Ins A100: A        → A(1,1,1)
2. Ins B90:  A,B      → A(1,1,1) B(2,2,2)
3. Ins C100: A,C,B    → A(1,1,1) C(2,1,1) B(3,3,2)
4. Ins D80:  A,C,B,D  → A(1,1,1) C(2,1,1) B(3,3,2) D(4,4,3)
5. Ins E90:  A,C,B,E,D→ A(1,1,1) C(2,1,1) B(3,3,2) E(4,3,2) D(5,5,3)
6. Del C:    A,B,E,D  → A(1,1,1) B(2,2,2) E(3,2,2) D(4,4,3)
(甲) 第3步 C=(2,1,1)；若把 RANK 错设成 ROW_NUMBER，C 的 RANK 错成 2。
(乙) 第3步 B: RANK=3、DENSE_RANK=2；若把 DENSE_RANK 错设成 RANK，则错成 3。
(丙) 第6步后 B=(2,2,2)、D=(4,4,3)；删除不调后继则 B 残留 ROW_NUMBER=3、RANK=3；DENSE_RANK 无条件 -1 则 B 错成 1。

## 四条不变量（代码保证位置 / 钉住的测试函数）
1 与批量重算一致：名次在 rank.(*Set).Get 中由顺序统计计数（pos/dpos）推导，api.verify 用 ord.Batch 全量核对，SelfCheck 复用之 / TestBatchConsistency
2 删除回退：rank.(*Set).Add/Remove 共用同一组有序切片与 cnt 计数，按同一规则逆向销账（cnt-1、归并摘除消失的 score）/ TestDeleteRollback
3 并列一致性：ord.Less 定义全序（score 降、id 升）；ROW_NUMBER=全序位置，RANK=1+(sc,"")前的元素数，DENSE_RANK=1+更高互异 score 数 / TestTieConsistency
4 失败不留痕：rank.(*Set).Add/Remove 先返回哨兵错误（ErrEmptyID/ErrDuplicateID/ErrIDNotFound），通过校验后才动状态 / TestRejectedOpsNoTrace
