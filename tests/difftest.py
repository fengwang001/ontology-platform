"""Differential testing: efficient scheduler vs independent naive model.

Run directly to execute random scenarios and print a per-step log of inputs,
outputs and the judgment basis, e.g.::

    python3 -m tests.difftest --cases 1200 --log diff.log
"""

from __future__ import annotations

import argparse
import dataclasses
import random

from mptc.errors import ErrCode, SchedulerError
from mptc.naive import NaiveScheduler
from mptc.scheduler import Scheduler
from mptc.types import SubflowRole

ROLES = (SubflowRole.NORMAL, SubflowRole.BACKUP)


@dataclasses.dataclass
class StepOutcome:
    index: int
    call: str
    emitted: tuple
    rejected: object


class ScenarioRunner:
    """Drives both implementations with the same event stream."""

    def __init__(self, seed, mss=10, log_lines=None):
        self.seed = seed
        self.mss = mss
        self.fast = Scheduler(mss)
        self.naive = NaiveScheduler(mss)
        self.rng = random.Random(seed)
        self.log = log_lines if log_lines is not None else []
        self.flow_ids = []
        self.step_index = 0
        self.log.append(
            f"# scenario seed={seed} mss={mss} "
            f"basis=inputs/outputs identical, snapshots identical, "
            f"rejection code+message identical"
        )

    # ------------------------------------------------------------- helpers

    def _record(self, call, fast_out, fast_err, naive_out, naive_err):
        self.step_index += 1

        def err_repr(err):
            return "-" if err is None else f"{err.code.value}({err.message})"

        def out_repr(out):
            if out is None:
                return "REJECTED"
            return (
                "["
                + ", ".join(
                    f"seq={s.seq},len={s.length}->{s.subflow}"
                    + ("(rein)" if s.reinjected else "")
                    for s in out.segments
                )
                + "]"
            )

        same_outputs = _same_segments(fast_out, naive_out)
        same_errors = (
            fast_err is None
            and naive_err is None
            or fast_err is not None
            and naive_err is not None
            and fast_err.code is naive_err.code
        )
        same_snapshot = self.fast.snapshot() == self.naive.snapshot()
        verdict = "OK" if (same_outputs and same_errors and same_snapshot) else "MISMATCH"
        self.log.append(
            f"[{self.step_index:04d}] {call} => {out_repr(fast_out)} "
            f"| naive={out_repr(naive_out)} | reject={err_repr(fast_err)} "
            f"| basis: out_eq={same_outputs} err_eq={same_errors} "
            f"state_eq={same_snapshot} => {verdict}"
        )
        if verdict != "OK":
            raise AssertionError(
                f"divergence at seed={self.seed} step={self.step_index}: {call}\n"
                f"fast_err={fast_err} naive_err={naive_err}\n"
                f"fast={self.fast.snapshot()}\nnaive={self.naive.snapshot()}"
            )

    def _invoke(self, name, args):
        call = f"{name}({', '.join(map(repr, args))})"
        fast_out = naive_out = None
        fast_err = naive_err = None
        try:
            fast_out = getattr(self.fast, name)(*args)
        except SchedulerError as exc:
            fast_err = exc
        try:
            naive_out = getattr(self.naive, name)(*args)
        except SchedulerError as exc:
            naive_err = exc
        self._record(call, fast_out, fast_err, naive_out, naive_err)

    # ----------------------------------------------------------- generator

    def random_event(self):
        rng = self.rng
        # Keep a small population so normal/backup switching and failures
        # happen frequently; sometimes inject invalid / unknown-id events.
        kind = rng.choice(
            [
                "add",
                "add",
                "write",
                "write",
                "sack",
                "sack",
                "cack",
                "cack",
                "fail",
                "recover",
                "rtt",
                "bad",
            ]
        )
        if kind == "add" and len(self.flow_ids) < 6:
            sid = f"f{rng.randrange(6)}"
            rtt = rng.choice([1, 5, 10, 10, 20])
            cwnd = rng.choice([self.mss, self.mss, self.mss * 2, self.mss * 3])
            role = rng.choice(ROLES)
            if sid not in self.flow_ids:
                self.flow_ids.append(sid)
            self._invoke("add_subflow", (sid, rtt, cwnd, role))
            return
        if kind == "write":
            size = rng.randrange(1, self.mss * 4 + 1)
            self._invoke("write", (b"x" * size,))
            return
        sid = rng.choice(self.flow_ids) if self.flow_ids else "ghost"
        if kind == "sack":
            # Often aligned multiples of mss, occasionally odd values to hit
            # non-aligned / out-of-range errors.
            amount = rng.choice(
                [0, self.mss, self.mss, self.mss * 2, rng.randrange(1, self.mss),
                 self.mss * 10]
            )
            self._invoke("subflow_ack", (sid, amount))
        elif kind == "cack":
            snap = self.fast.snapshot()
            sent = snap["sent_max_end"]
            acked = snap["acked_seq"]
            if sent > 0:
                delta = rng.choice(
                    [0, self.mss, max(1, self.mss - 1), rng.randrange(0, sent + 1)]
                )
                ack_seq = min(sent, acked + delta)
                if rng.random() < 0.15 and acked > 0:
                    ack_seq = acked - 1  # stale
            else:
                ack_seq = rng.choice([0, 1])
            window = rng.choice(
                [0, self.mss - 1, self.mss, self.mss, self.mss * 2,
                 self.mss * 10]
            )
            self._invoke("connection_ack", (ack_seq, window))
        elif kind == "fail":
            self._invoke("subflow_fail", (sid,))
        elif kind == "recover":
            self._invoke("subflow_recover", (sid,))
        elif kind == "rtt":
            self._invoke("change_rtt", (sid, rng.choice([1, 5, 10, 40])))
        else:
            self._bad_event()

    def _bad_event(self):
        rng = self.rng
        flavor = rng.randrange(6)
        if flavor == 0:
            self._invoke("add_subflow", ("", 10, self.mss, SubflowRole.NORMAL))
        elif flavor == 1:
            self._invoke("add_subflow", ("z", 0, self.mss, SubflowRole.NORMAL))
        elif flavor == 2:
            self._invoke("add_subflow", ("z", 10, self.mss - 1, SubflowRole.NORMAL))
        elif flavor == 3:
            self._invoke("write", (b"",))
        elif flavor == 4:
            self._invoke("change_rtt", ("missing", 10))
        else:
            self._invoke("subflow_ack", (42, 0))


def _same_segments(a, b):
    if a is None or b is None:
        return a is b
    return [dataclasses.astuple(s) for s in a.segments] == [
        dataclasses.astuple(s) for s in b.segments
    ]


def run_case(seed, steps, mss, log):
    runner = ScenarioRunner(seed, mss=mss, log_lines=log)
    for _ in range(steps):
        runner.random_event()
    return runner


def main(argv=None):
    parser = argparse.ArgumentParser()
    parser.add_argument("--cases", type=int, default=1200)
    parser.add_argument("--steps", type=int, default=120)
    parser.add_argument("--seed", type=int, default=20261006)
    parser.add_argument("--log", default="diff.log")
    args = parser.parse_args(argv)

    log = []
    rng = random.Random(args.seed)
    for case in range(args.cases):
        seed = rng.getrandbits(63)
        mss = rng.choice([3, 5, 10, 10, 64])
        log.append(f"=== case {case + 1}/{args.cases} ===")
        run_case(seed, args.steps, mss, log)
    with open(args.log, "w", encoding="utf-8") as handle:
        handle.write("\n".join(log) + "\n")
    print(f"OK: {args.cases} cases x {args.steps} steps; log -> {args.log}")


if __name__ == "__main__":
    main()
