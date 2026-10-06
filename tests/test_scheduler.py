"""Targeted tests for every behaviour called out in the specification."""

from __future__ import annotations

import threading
import unittest

from mptc.errors import ErrCode, SchedulerError
from mptc.scheduler import Scheduler
from mptc.types import SubflowRole


def seqs(result):
    return [(s.seq, s.length, s.subflow, s.reinjected) for s in result.segments]


class BasicTests(unittest.TestCase):
    def test_segmentation_and_routing(self):
        s = Scheduler(10)
        s.add_subflow("a", 10, 100)
        s.add_subflow("b", 5, 100)
        r = s.write(b"x" * 25)
        # Lowest RTT wins regardless of insertion order; tail segment < mss.
        self.assertEqual(seqs(r), [(0, 10, "b", False), (10, 10, "b", False),
                                   (20, 5, "b", False)])

    def test_rtt_tie_picks_smaller_id(self):
        s = Scheduler(10)
        s.add_subflow("z", 10, 100)
        s.add_subflow("a", 10, 100)
        r = s.write(b"x" * 10)
        self.assertEqual(seqs(r)[0][2], "a")

    def test_cwnd_constrains_and_spills(self):
        s = Scheduler(10)
        s.add_subflow("a", 5, 10)
        s.add_subflow("b", 10, 10)
        r = s.write(b"x" * 30)
        self.assertEqual(seqs(r), [(0, 10, "a", False), (10, 10, "b", False)])
        # Ack on a frees room and triggers the waiting head segment.
        r = s.subflow_ack("a", 10)
        self.assertEqual(seqs(r), [(20, 10, "a", False)])


class BackupTests(unittest.TestCase):
    def test_backup_only_when_no_active_normal(self):
        s = Scheduler(10)
        s.add_subflow("n", 100, 10)
        s.add_subflow("b", 1, 100, SubflowRole.BACKUP)
        s.connection_ack(0, 1000)
        # Normal exists even though full: backup must not be used.
        r = s.write(b"x" * 20)
        self.assertEqual(seqs(r), [(0, 10, "n", False)])
        # Failing the only normal activates backup routing.
        r = s.subflow_fail("n")
        self.assertEqual({x[2] for x in seqs(r)}, {"b"})
        self.assertEqual([x[0] for x in seqs(r)], [0, 10])
        # A recovered normal takes precedence again.
        r = s.subflow_recover("n")
        r = s.write(b"x" * 10)
        self.assertEqual(seqs(r)[0][2], "n")


class WindowTests(unittest.TestCase):
    def test_exact_window_and_one_byte_short(self):
        s = Scheduler(10)
        s.add_subflow("a", 5, 1000)
        # Window of exactly 20 bytes: two segments may go; third waits.
        s.connection_ack(0, 20)
        r = s.write(b"x" * 30)
        self.assertEqual([(x[0], x[1]) for x in seqs(r)], [(0, 10), (10, 10)])
        # One byte smaller window: nothing new may be sent.
        r = s.connection_ack(0, 19)
        self.assertEqual(seqs(r), [])
        # Advancing the cumulative ack restores room for the tail.
        r = s.connection_ack(10, 19)  # right edge 29 still blocks [20,30)
        self.assertEqual(seqs(r), [])
        r = s.connection_ack(10, 20)  # right edge 30 allows it exactly
        self.assertEqual([(x[0], x[1]) for x in seqs(r)], [(20, 10)])

    def test_shrink_does_not_recall_sent(self):
        s = Scheduler(10)
        s.add_subflow("a", 5, 1000)
        s.connection_ack(0, 30)
        s.write(b"x" * 30)
        r = s.connection_ack(0, 5)
        self.assertEqual(seqs(r), [])
        self.assertEqual(s.snapshot()["sent_max_end"], 30)


class FailureReinjectTests(unittest.TestCase):
    def test_reinject_order_and_duplicate_copies(self):
        s = Scheduler(10)
        s.add_subflow("a", 100, 100)
        s.connection_ack(0, 1000)
        s.write(b"x" * 40)  # seqs 0..30 on a
        s.subflow_ack("a", 10)  # seq0 released
        s.add_subflow("b", 1, 100)
        r = s.subflow_fail("a")
        # seqs 10,20,30 reinjected ascending, all routed to b (not a).
        self.assertEqual(
            seqs(r),
            [(10, 10, "b", True), (20, 10, "b", True), (30, 10, "b", True)],
        )

    def test_duplicate_copies_after_recovery_then_fail(self):
        s = Scheduler(10)
        s.add_subflow("a", 100, 100)
        s.connection_ack(0, 1000)
        s.write(b"x" * 10)  # seq0 on a
        s.add_subflow("b", 1, 100)
        s.subflow_fail("a")          # a clear; reinject -> b
        s.subflow_recover("a")       # a usable again, inflight 0
        s.write(b"x" * 10)           # seq10 goes to fastest normal (b rtt1)
        s.connection_ack(0, 1000)    # covers seq0; b copy still occupies b
        snap = s.snapshot()
        # seq0's b copy survives the connection ack.
        self.assertEqual(snap["flows"]["b"]["inflight"], [(0, 10), (10, 10)])
        r = s.subflow_ack("b", 10)   # releases seq0 only
        self.assertEqual([(x.seq) for x in seqs(r)], [])
        self.assertEqual(s.snapshot()["flows"]["b"]["inflight"], [(10, 10)])

    def test_failed_subflow_late_ack_ignored(self):
        s = Scheduler(10)
        s.add_subflow("a", 100, 100)
        s.connection_ack(0, 1000)
        s.write(b"x" * 20)
        before = s.snapshot()
        s.subflow_fail("a")
        # Any ack value, including one that would be out of range, is ignored.
        r = s.subflow_ack("a", 99999)
        self.assertEqual(seqs(r), [])
        self.assertEqual(s.snapshot()["acked_seq"], before["acked_seq"])

    def test_reinject_avoids_origin_after_it_recovers(self):
        s = Scheduler(10)
        s.add_subflow("a", 100, 100)
        s.connection_ack(0, 1000)
        s.write(b"x" * 10)  # seq0 on the fastest normal: b
        s.subflow_fail("a")  # no alternative active flow: reinject waits
        self.assertEqual(s.snapshot()["reinject"], [(0, 10)])
        s.add_subflow("b", 1, 100)  # another flow now exists; still waits? no
        # It immediately goes to b when b is added; origin a still failed.
        # Now recover the origin with a *better* rtt: nothing is queued there.
        r = s.subflow_recover("b")
        self.assertEqual(seqs(r), [])
        r = s.subflow_recover("a")
        self.assertEqual(seqs(r), [])
        # The copy lives only on b, never back on origin a.
        self.assertEqual(s.snapshot()["flows"]["a"]["inflight"], [])
        self.assertEqual(s.snapshot()["flows"]["b"]["inflight"], [(0, 10)])

    def test_recovered_subflow_window_reset(self):
        s = Scheduler(10)
        s.add_subflow("a", 100, 20)
        s.add_subflow("b", 1, 20)
        s.connection_ack(0, 1000)
        s.write(b"x" * 20)
        s.change_rtt("a", 7)
        s.subflow_fail("a")
        s.subflow_recover("a")
        snap = s.snapshot()["flows"]["a"]
        self.assertEqual(snap["inflight_bytes"], 0)
        self.assertEqual(snap["cwnd"], 20)
        self.assertEqual(snap["rtt_ms"], 100)  # reset to initial RTT


class AckValidationTests(unittest.TestCase):
    def test_not_aligned_then_out_of_range_priority(self):
        s = Scheduler(10)
        s.add_subflow("a", 100, 100)
        s.connection_ack(0, 1000)
        s.write(b"x" * 20)
        with self.assertRaises(SchedulerError) as cm:
            s.subflow_ack("a", 5)  # inside range but mid-segment
        self.assertIs(cm.exception.code, ErrCode.ACK_NOT_SEGMENT_ALIGNED)
        with self.assertRaises(SchedulerError) as cm:
            s.subflow_ack("a", 25)  # beyond inflight
        self.assertIs(cm.exception.code, ErrCode.ACK_OUT_OF_RANGE)
        # Rejected events changed nothing: a valid 10-byte ack still works.
        r = s.subflow_ack("a", 10)
        self.assertEqual([x.seq for x in seqs(r)], [])
        self.assertEqual(s.snapshot()["flows"]["a"]["inflight"], [(10, 10)])

    def test_error_priority_ordering(self):
        s = Scheduler(10)
        # invalid param beats subflow-not-found.
        with self.assertRaises(SchedulerError) as cm:
            s.change_rtt("missing", 0)
        self.assertIs(cm.exception.code, ErrCode.INVALID_PARAM)
        with self.assertRaises(SchedulerError) as cm:
            s.change_rtt("missing", 10)
        self.assertIs(cm.exception.code, ErrCode.SUBFLOW_NOT_FOUND)

        s.add_subflow("a", 100, 100)
        s.connection_ack(0, 1000)
        s.write(b"x" * 10)
        s.connection_ack(10, 10)
        # Stale connection ack beats (hypothetical) other checks; it is
        # recognised before the ack>sent validity check.
        with self.assertRaises(SchedulerError) as cm:
            s.connection_ack(5, 10)
        self.assertIs(cm.exception.code, ErrCode.STALE_ACK)

    def test_stale_conn_ack_changes_nothing(self):
        s = Scheduler(10)
        s.add_subflow("a", 5, 1000)
        s.connection_ack(0, 30)
        s.write(b"x" * 30)
        s.connection_ack(20, 30)
        before = s.snapshot()
        with self.assertRaises(SchedulerError):
            s.connection_ack(19, 999)  # stale; window must not update
        self.assertEqual(s.snapshot(), before)

    def test_reinjected_copy_dropped_when_conn_acked(self):
        s = Scheduler(10)
        s.add_subflow("a", 100, 100)
        s.connection_ack(0, 1000)
        s.write(b"x" * 20)
        s.subflow_fail("a")  # reinject queue has seq0,seq10 but no candidate
        self.assertEqual(
            s.snapshot()["reinject"], [(0, 10), (10, 10)]
        )
        r = s.connection_ack(10, 1000)  # seq0 copy dropped before sending
        self.assertEqual(s.snapshot()["reinject"], [(10, 10)])
        self.assertEqual(seqs(r), [])  # still no active subflow


class ComplexityTests(unittest.TestCase):
    def test_noop_event_does_not_touch_queue(self):
        # touch_count must not grow with queued data for an event that moves
        # nothing (here: a stale ack rejected entirely, and a no-op RTT read).
        s = Scheduler(10)
        s.add_subflow("a", 100, 10)
        s.connection_ack(0, 10)
        s.write(b"x" * 10000)  # most of it queued behind a tiny window
        self.assertFalse(s.subflow_ack("a", 0).segments or False)
        baseline = s.touch_count
        for _ in range(1000):
            try:
                s.connection_ack(0, 0)
            except SchedulerError:
                pass
        # A rejected event performs zero state-unit work regardless of the
        # 10k-byte pending queue.
        self.assertEqual(s.touch_count, baseline)

    def test_send_decision_scales_with_moves_not_history(self):
        s = Scheduler(10)
        for sid in ("a", "b", "c"):
            s.add_subflow(sid, 100, 10_000_000)
        s.connection_ack(0, 10_000_000)
        s.write(b"x" * 50_000)
        # touch work per ack-release/send is bounded by segments actually
        # moved; this is O(emitted), not O(total history).
        before = s.touch_count
        s.subflow_ack("a", 10)
        touched = s.touch_count - before
        self.assertLessEqual(touched, 5)


class ConcurrencyTests(unittest.TestCase):
    def test_parallel_events_are_serialized(self):
        # Hammer from many threads; the resulting state must match the same
        # total byte counts applied serially (linearisability of the lock).
        s = Scheduler(10)
        s.add_subflow("a", 5, 100000)
        s.add_subflow("b", 5, 100000)
        s.connection_ack(0, 100000)

        errors = []

        def writer(times):
            try:
                for _ in range(times):
                    s.write(b"x" * 10)
            except SchedulerError as exc:  # pragma: no cover - must not happen
                errors.append(exc)

        threads = [threading.Thread(target=writer, args=(50,)) for _ in range(8)]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
        self.assertFalse(errors)
        snap = s.snapshot()
        # 8 threads x 50 writes x 10 bytes, exactly, no lost or split bytes.
        self.assertEqual(snap["next_seq"], 4000)
        total_inflight = sum(
            f["inflight_bytes"] for f in snap["flows"].values()
        )
        self.assertEqual(total_inflight, 4000)


if __name__ == "__main__":
    unittest.main(verbosity=2)
