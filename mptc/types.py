"""Public value types used by the scheduler API."""

from __future__ import annotations

import dataclasses
import enum


class SubflowRole(enum.Enum):
    NORMAL = "normal"
    BACKUP = "backup"


@dataclasses.dataclass(frozen=True)
class SentSegment:
    """One segment emitted by a send decision."""

    seq: int
    length: int
    subflow: str
    reinjected: bool = False


@dataclasses.dataclass(frozen=True)
class Result:
    """Segments produced by the send decision following one event."""

    segments: tuple[SentSegment, ...] = ()
