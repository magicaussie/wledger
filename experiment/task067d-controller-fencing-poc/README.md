# Task 067D controller-fencing prototype (host-only)

**Experimental / test-only. Not WLED firmware, not linked to WLEDger production code, and not hardware-validated.**

The header models an apply-point fencing state machine behind abstract journal and pixel-output interfaces. The test doubles exercise generation/sequence checks, exact command-ID retries, boot epochs, persistence failure, identity/layout matching, output bounds, owned-range isolation, and partial multi-controller acknowledgement.

The `Journal::commit` contract assumes an atomic durable record replacement. The in-memory test double can reject a commit but does not emulate flash, NVS, torn writes, wear, a real reboot, WLED concurrency, effects, network receive paths, or LED-driver transfer completion. The prototype intentionally contains no ESP-specific storage implementation and no WLED core patch.

## Host build and test

From the repository root, with a C++17 compiler:

```sh
g++ -std=c++17 -Wall -Wextra -Werror -pedantic -fsanitize=address,undefined \
  experiment/task067d-controller-fencing-poc/fence_state_machine_test.cpp \
  -o /tmp/task067d-fence-test
/tmp/task067d-fence-test
```

A passing result validates only this host model. It must not be described as a real-firmware or physical-output PASS.
