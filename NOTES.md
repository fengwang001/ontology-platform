# NOTES — ontology-289 KTable FK join

记号: Ax=(A,x), Bx=(B,x), By=(B,y), Nx=("",x)；响应=(k,哈希,RV/nil)。列: 步|订阅|Q_A|Q_B|判定|本步日志|结果。
1|A{k1:Ax}|[k1 Ax a1]|[]|—|无|{}
2|B{k1:Bx}|[k1 Ax a1]|[k1 Bx b1]|—|无|{}
3|B{k1:Bx}|[k1 Ax a1]|[]|采纳|+k1=(x,b1)|{k1:(x,b1)}
4|B{k1:Bx}|[]|[]|丢弃|无|{k1:(x,b1)}
5|B{k1:Bx}|[]|[k1 Bx b2]|—|无|{k1:(x,b1)}
6|无|[]|[k1 Bx b2]|—|-k1|{}
7|无|[]|[]|丢弃|无|{}
8|B{k2:By}|[]|[k2 By b2]|—|无|{}
9|B{k2:By};右仅余A|[]|[k2 By b2, k2 By nil]|—|无|{}
10|B{k2:By}|[]|[k2 By nil]|采纳|+k2=(y,b2)|{k2:(y,b2)}
11|B{k2:By}|[]|[]|采纳|-k2|{}
甲: 步4投递的是步1产生的(k1,Ax,a1)，当前左行哈希Bx≠Ax故丢弃；若不校验哈希，步4输出+ k1=(x,a1)、结果{k1:(x,a1)}，而k1的FK此刻已是B(幻影连到A)。
乙: 步6立即输出-k1(FK置NULL不订阅)；步7投递的是步5 PutRight(B,b2)产生的(k1,Bx,b2)，当前哈希Nx≠Bx故丢弃；不校验哈希则11步后结果={k1:(x,b2)}(NULL行幻影)。
丙: 步9只入队nil响应且保留订阅，结果表只在投递时改变；k2在步10出现(+k2=(y,b2)，步8订阅时的快照值，哈希仍有效故采纳)、步11撤回(-k2)；nil若“不动作”，步11无输出、结果残留{k2:(y,b2)}，违反不变量1。

不变量|代码保证位置|钉住的测试函数
1 与批量重算一致|lside.go deliver 哈希校验+nil撤回/drainAll|TestBatchRecomputation、TestElevenStepScenario
2 变更日志前缀自洽|lside.go emitAdd/emitDel 输出前比对结果现值|TestChangelogPrefixes、TestElevenStepScenario
3 订阅一致|lside.go PutLeft/DeleteLeft 配对 rside.go Subscribe/RemoveSub；采纳必过哈希相等|TestSubscriptionConsistency
4 失败不留痕|lside.go 任何改状态前预校验空键/空队列/pending额度|TestSentinelErrorsAtomic
复杂度: rside.go lastChecked 非导出、无导出读口，仅同包测试 TestSubscriberCheckCountScalesWithFKOnly 读取。
