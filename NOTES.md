# HRW NOTES

## 三、推导（权重取题给固定表）

1. ①加k1：w(A)=10 最大 → k1→A
2. ②加k2：w(B)=9 最大 → k2→B（k1 仍→A）
3. ③加k3：A、B 均 6 并列，取 ID 字典序小 → k3→A
4. ④加k4：w(C)=9 最大 → k4→C；此刻 k1:A k2:B k3:A k4:C
5. ⑤AddNode(D)：仅 k1 新权 11>10，A→D；k2(3<9)留B、k3(6=6 且 D>A)留A、k4(7<9)留C
6. ⑥RemoveNode(A)：A 仅持 k3，在 B/C/D 重算，B、D 均 6 取小 → k3 A→B；余不动。末态 k1:D k2:B k3:B k4:C

（甲）错成「并列取 ID 最大」：k3→B；错成「后扫描到的覆盖」（序 A→B→C，等权也覆盖）：k3→B。正确答案是 A。
（乙）mod3：k1=10→B、k2=13→B、k3=11→C、k4=5→C；mod4：k1→C、k2→B、k3→D、k4→A。
　　共 3 次搬迁：k1 B→C、k3 C→D、k4 C→A；HRW 仅 k1 A→D。多余 2 次（k3、k4），且 k1 去向也错（C 而非 D）。
（丙）每次调用重新播种：⑥后反复 Owner(k3) 在 B/C/D 间横跳；违反不变量 3（确定性），并连带破坏不变量 1。

## 二、不变量：代码保证位置 / 钉住它的测试

- I1 朴素一致：归属判定唯一来源 rnd.Pick（rnd/rnd.go，含并列取 ID 小），cluster 的 Owner/重定位全走它 → TestNaiveConsistencyAfterOps、TestAPIOwnerMatchesNaive、TestSelfCheck
- I2 最小搬迁：AddNode 仅当 rnd.Prefer（新权更大，或等权且新 ID 更小）才改归属；RemoveNode 只遍历 owned[id] → TestMinimalRelocation、TestRemoveRelocationCount
- I3 确定性：rnd.Weight 为纯 FNV-1a（长度前缀定界，无时间/随机/PID）；Owner 只取决于 (w,id)，与加入顺序无关 → TestDeterminism、TestConcurrentOwners
- I4 失败不留痕：cluster 三方法均先校验后改任何 map，哨兵错误互不相同 → TestFailureNoTrace、TestSentinelErrors
