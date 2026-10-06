"""Empirical complexity check.

Verifies that per-event work does not grow with queued data or history:
1. A rejected (stale) event performs zero state-unit touches regardless of a
   100k-byte pending queue.
2. An event that moves k segments does O(k) work, measured while the total
   history grows 32x.
"""

from __future__ import annotations

import time

from mptc.errors import SchedulerError
from mptc.scheduler import Scheduler


def rejected_event_cost():
    rows = []
    for size in (1_000, 10_000, 100_000):
        s = Scheduler(10)
        s.add_subflow("a", 1, 10)
        s.connection_ack(0, size + 100)
        s.write(b"x" * size)
        s.connection_ack(10, size + 100)  # advance cumulative ack
        base = s.touch_count
        start = time.perf_counter_ns()
        for _ in range(1000):
            try:
                s.connection_ack(9, 0)  # strictly smaller -> stale, rejected
            except SchedulerError:
                pass
        elapsed_ns = time.perf_counter_ns() - start
        rows.append((size, s.touch_count - base, elapsed_ns / 1000))
    return rows


def moving_event_cost():
    rows = []
    for history in (10_000, 80_000, 320_000):
        s = Scheduler(10)
        s.add_subflow("a", 1, history + 100)
        s.add_subflow("b", 1, history + 100)
        s.connection_ack(0, history + 100)
        s.write(b"x" * history)
        base = s.touch_count
        # Release exactly one segment and let the decision resend one.
        s.subflow_ack("a", 10)
        touched = s.touch_count - base
        rows.append((history, touched))
    return rows


def main():
    print("rejected/stale event cost (queue_bytes, touches, avg_ns):")
    for row in rejected_event_cost():
        print(" ", row)
    print("single-segment move cost (history_bytes, touches):")
    for row in moving_event_cost():
        print(" ", row)


if __name__ == "__main__":
    main()
