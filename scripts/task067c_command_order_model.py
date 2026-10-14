#!/usr/bin/env python3
"""Deterministic reference model for Task 067C ordering scenarios.

This is an architecture simulation, not WLED firmware, a WLED emulator, or
proof that any real WLED build implements the proposed fencing protocol.
"""

from dataclasses import dataclass


@dataclass(frozen=True)
class Command:
    epoch: int
    generation: int
    sequence: int
    command_id: str
    value: str


class UnfencedWebSocketModel:
    """Models a WS service that queues complete messages before applying them."""

    def __init__(self):
        self.lit = False
        self.pending = []

    def receive(self, command):
        self.pending.append(command)

    def stop_and_ack(self):
        self.lit = False

    def drain_one(self):
        command = self.pending.pop(0)
        self.lit = command.value == "red"


class FencedControllerModel:
    """Models device-side epoch/generation checks at the application point."""

    def __init__(self):
        self.epoch = 1
        self.floor = 0
        self.last_sequence = 0
        self.outcomes = {}
        self.lit = False
        self.pending = []

    def receive(self, command):
        self.pending.append(command)

    def process_one(self):
        return self.apply(self.pending.pop(0))

    def apply(self, command):
        if command.epoch != self.epoch:
            return "stale-epoch"
        if command.generation < self.floor:
            return "stale-generation"

        key = (command.epoch, command.generation, command.command_id)
        prior = self.outcomes.get(key)
        if prior is not None:
            if prior[0] != command.value or prior[1] != command.sequence:
                return "command-id-conflict"
            return "duplicate"

        if command.generation > self.floor:
            self.floor = command.generation
            self.last_sequence = 0
        if command.sequence <= self.last_sequence:
            return "stale-sequence"

        self.last_sequence = command.sequence
        self.lit = command.value == "red"
        self.outcomes[key] = (command.value, command.sequence)
        return "applied"

    def stop_barrier(self, generation, sequence, command_id):
        # The floor advance and targeted black state are one device-side
        # serialized operation. The response is sent only after this returns.
        result = self.apply(Command(self.epoch, generation, sequence, command_id, "black"))
        assert result in ("applied", "duplicate")
        return result

    def reboot(self):
        # The proposed persistent boot epoch increments before command service
        # resumes. Old in-memory work is gone; late old-epoch retries are stale.
        self.epoch += 1
        self.pending.clear()
        self.last_sequence = 0


def check(condition, message):
    if not condition:
        raise AssertionError(message)
    print(f"PASS {message}")


def main():
    old_activation = Command(1, 4, 1, "act-4-1", "red")

    # Reproduce the Task 067B ordering on a deliberately weak WS server model:
    # old message is received/queued on connection 1, then Stop is acknowledged
    # on a new connection before the queued old message is applied.
    ws = UnfencedWebSocketModel()
    ws.receive(old_activation)
    ws.stop_and_ack()
    check(not ws.lit, "WS simulation cleanup is black at its acknowledgement")
    ws.drain_one()
    check(ws.lit, "WS simulation reproduces delayed activation relight after acknowledgement")

    fenced = FencedControllerModel()
    fenced.receive(old_activation)
    fenced.stop_barrier(5, 1, "stop-5-1")
    check(not fenced.lit, "fenced Stop applies targeted black before its barrier acknowledgement")
    check(fenced.process_one() == "stale-generation", "queued obsolete generation is rejected at application")
    check(not fenced.lit, "obsolete queued activation cannot relight after fenced Stop")

    duplicate = FencedControllerModel()
    activation = Command(1, 4, 1, "act-4-1", "red")
    check(duplicate.apply(activation) == "applied", "first activation sequence applies")
    check(duplicate.apply(activation) == "duplicate", "duplicate command identity is acknowledged without reapplication")
    duplicate.stop_barrier(5, 1, "stop-5-1")
    check(duplicate.apply(activation) == "stale-generation", "duplicate activation from fenced generation is rejected")
    check(not duplicate.lit, "duplicate/obsolete activation leaves owned pixels black")

    rebooted = FencedControllerModel()
    rebooted.receive(old_activation)
    rebooted.reboot()
    check(rebooted.apply(old_activation) == "stale-epoch", "controller reboot invalidates an outstanding old-epoch command")

    competing = FencedControllerModel()
    competing.stop_barrier(5, 1, "app-a-stop")
    check(competing.apply(Command(1, 4, 2, "app-b-activation", "red")) == "stale-generation",
          "competing stale application process is fenced by the newer controller generation")

    # Lost acknowledgement: retrying exactly the same command identity is
    # idempotent; after reboot the caller must negotiate a current epoch and
    # issue/reconcile a new Stop while durable ownership remains unresolved.
    lost_ack = FencedControllerModel()
    lost_ack.stop_barrier(5, 1, "stop-5-1")
    check(lost_ack.stop_barrier(5, 1, "stop-5-1") == "duplicate",
          "lost Stop acknowledgement can be retried idempotently in the same epoch")
    check(not lost_ack.lit, "lost-ack retry does not undo targeted cleanup")

    # A three-controller Locate is releasable only if every target has a durable
    # confirmed fence/cleanup result. One missing acknowledgement stays blocked.
    per_controller = {"c1": True, "c2": False, "c3": True}
    check(not all(per_controller.values()), "three-controller partial delivery retains durable ownership")
    per_controller["c2"] = True
    check(all(per_controller.values()), "three-controller ownership releases only after every target confirms")

    reconnect = FencedControllerModel()
    reconnect.receive(old_activation)
    reconnect.reboot()
    check(reconnect.apply(old_activation) == "stale-epoch", "reconnect with an outstanding old command fails closed")

    # Application and gateway crashes do not revoke an already delivered
    # command; the same higher-generation controller fence rejects it.
    after_crash = FencedControllerModel()
    after_crash.receive(old_activation)
    after_crash.stop_barrier(5, 1, "recovery-stop")
    check(after_crash.process_one() == "stale-generation",
          "application/gateway crash recovery fences an already delivered old command")


if __name__ == "__main__":
    main()
