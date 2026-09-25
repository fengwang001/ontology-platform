# NOTES
六作业调度表：X(0,5) L(1,10) A(2,2) B(3,3) C(4,2) D(5,1)
1. t=0  就绪{X:5}                 选 X 区间[0,5)
2. t=5  就绪{L:10,A:2,B:3,C:2,D:1} 选 D 区间[5,6)
3. t=6  就绪{L:10,A:2,B:3,C:2}     选 A 区间[6,8)（A/C 同长，A 注册更早）
4. t=8  就绪{L:10,B:3,C:2}         选 C 区间[8,10)
5. t=10 就绪{L:10,B:3}             选 B 区间[10,13)
6. t=13 就绪{L:10}                 选 L 区间[13,23)
完成顺序 [X,D,A,C,B,L]。
(甲) FCFS 在 t=5 会选最早到达的 L 而非最短的 D，完成顺序错成 [X,L,A,B,C,D]（L[5,15)）。
(乙) 抢占式 SRTF 在 t=2：A 剩余 2 < X 剩余 3，只跑了 2 tick 的 X 被抢占；完成顺序错成 [A,C,D,X,B,L]。
(丙) t=6 若平局按"后到先执行"，则 C 先 A 后，错成 [X,D,C,A,B,L]。L 在 t=1 就绪、t=13 才开始，等待 12 tick；SJF 总优先新来的短作业，短作业流不断时 L 无限靠后即饿死。
不变量保证位置 / 钉住的测试函数：
1 与朴素参照一致：sched/sched.go 的 Run/next 事件模拟，api/api.go 的 naiveRef 逐 tick 扫描比对、SelfCheck 核验；TestNaiveEquivalence
2 非抢占：sjob/sjob.go 的 Job.Begin 一次跑满 length（start→start+length），sched.next 选中后跑完才再次选作业；TestNonPreemptive
3 选优与注册序平局：sjob/sjob.go 的 Less 比 (length,reg)，sched/sched.go 用 container/heap 取最小；TestSelectionAndTie
4 失败不留痕：api/api.go 的 Submit 全部校验通过前不改任何状态，重复 id 在 sched.Add 落库前判定；TestRejectionLeavesNoTrace
