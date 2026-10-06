"""Runs the 1000+ random-event differential campaigns under unittest."""

from __future__ import annotations

import os
import unittest

from tests.difftest import run_case

CASE_COUNT = 1100
STEP_COUNT = 120
BASE_SEED = 20261006


class DifferentialTests(unittest.TestCase):
    def test_one_thousand_plus_random_campaigns(self):
        import random

        rng = random.Random(BASE_SEED)
        failures = []
        for _ in range(CASE_COUNT):
            seed = rng.getrandbits(63)
            mss = rng.choice([3, 5, 10, 10, 64])
            try:
                run_case(seed, STEP_COUNT, mss, [])
            except AssertionError as exc:  # pragma: no cover
                failures.append(str(exc))
                if len(failures) >= 3:
                    break
        self.assertEqual(failures, [], msg="\n".join(failures))

    def test_campaign_logs_every_step(self):
        # The same framework is used to produce the artefact required by the
        # spec: per-step input, output and judgment basis.
        log = []
        run_case(BASE_SEED, 25, 10, log)
        header = log[0]
        steps = [line for line in log if line.startswith("[")]
        self.assertIn("basis=", header)
        self.assertEqual(len(steps), 25)
        self.assertTrue(all("=> OK" in line for line in steps))
        if os.environ.get("MPTC_DIFF_LOG"):
            with open(os.environ["MPTC_DIFF_LOG"], "w", encoding="utf-8") as fh:
                fh.write("\n".join(log) + "\n")


if __name__ == "__main__":
    unittest.main(verbosity=2)
