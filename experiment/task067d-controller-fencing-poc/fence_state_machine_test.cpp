#include "fence_state_machine.hpp"

#include <iostream>
#include <stdexcept>

using namespace task067d;

namespace {

void require(bool condition, const char* message) {
  if (!condition) throw std::runtime_error(message);
}

class MemoryJournal final : public Journal {
 public:
  bool load(DurableRecord& record) const override {
    if (!present) return false;
    record = value;
    return true;
  }

  bool commit(const DurableRecord& record) override {
    if (fail_next_commit) {
      fail_next_commit = false;
      return false;
    }
    value = record;
    present = true;
    return true;
  }

  DurableRecord value;
  bool present = false;
  bool fail_next_commit = false;
};

class MemoryOutput final : public PixelOutput {
 public:
  explicit MemoryOutput(std::uint32_t count) : pixels(count) {
    for (std::size_t index = 0; index < pixels.size(); ++index) {
      pixels[index] = Color{static_cast<std::uint8_t>(20 + index),
                            static_cast<std::uint8_t>(40 + index),
                            static_cast<std::uint8_t>(60 + index), 7};
    }
  }

  bool writeRange(std::uint32_t output_id, std::uint32_t start,
                  std::uint32_t stop, Color color) override {
    if (output_id != 7 || start >= stop || stop > pixels.size()) return false;
    writes++;
    for (std::uint32_t index = start; index < stop; ++index) pixels[index] = color;
    return true;
  }

  std::vector<Color> pixels;
  std::size_t writes = 0;
};

Identity testIdentity() {
  return Identity{"installation-test", "controller-test", "layout-sha-test"};
}

DeviceLayout testLayout() {
  return DeviceLayout{"layout-sha-test", {{7, 16}}, {{7, 2, 6}, {7, 9, 12}}};
}

Command makeCommand(const Identity& identity, std::uint64_t epoch,
                    std::uint64_t generation, std::uint64_t sequence,
                    const char* command_id, CommandKind kind,
                    PixelRange range, Color color) {
  Command result;
  result.installation_id = identity.installation_id;
  result.controller_id = identity.controller_id;
  result.output_fingerprint = identity.output_fingerprint;
  result.boot_epoch = epoch;
  result.generation = generation;
  result.sequence = sequence;
  result.command_id = command_id;
  result.kind = kind;
  result.ranges.push_back(range);
  result.color = color;
  return result;
}

void testDelayedActivationAfterStop() {
  const Identity identity = testIdentity();
  MemoryJournal journal;
  journal.value = provisionedRecord(identity);
  journal.present = true;
  MemoryOutput output(16);
  FencedController controller(identity, testLayout(), journal, output);
  require(controller.boot(), "F01 controller boot failed");
  require(controller.apply(makeCommand(identity, controller.bootEpoch(), 0, 1,
                                       "boot-recovery-stop", CommandKind::Stop,
                                       {7, 2, 6}, {})).status == Status::Applied,
          "F01 post-boot recovery Stop failed");
  const auto initial = output.pixels;
  const Color red{255, 0, 0, 0};
  const Command activation = makeCommand(identity, controller.bootEpoch(), 4, 1,
                                         "activate-4-1", CommandKind::Activate,
                                         {7, 2, 6}, red);
  require(controller.apply(activation).status == Status::Applied,
          "F01 activation did not apply");
  const Command stop = makeCommand(identity, controller.bootEpoch(), 5, 1,
                                   "stop-5-1", CommandKind::Stop,
                                   {7, 2, 6}, {});
  const Acknowledgement stop_ack = controller.apply(stop);
  require(stop_ack.status == Status::Applied &&
              stop_ack.controller_id == identity.controller_id &&
              stop_ack.command_id == "stop-5-1" && stop_ack.generation == 5,
          "F01 Stop acknowledgement identity is incomplete");
  require(controller.apply(activation).status == Status::StaleGeneration,
          "F01 delayed old activation was not rejected");
  for (std::size_t index = 0; index < output.pixels.size(); ++index) {
    const Color expected = index >= 2 && index < 6 ? Color{} : initial[index];
    require(output.pixels[index] == expected,
            "F01 changed pixels outside targeted cleanup");
  }
  std::cout << "PASS F01 delayed activation fenced; targeted range only\n";
}

void testDuplicateAndCommandIdConflict() {
  const Identity identity = testIdentity();
  MemoryJournal journal;
  journal.value = provisionedRecord(identity);
  journal.present = true;
  MemoryOutput output(16);
  FencedController controller(identity, testLayout(), journal, output);
  require(controller.boot(), "F02 controller boot failed");
  require(controller.apply(makeCommand(identity, controller.bootEpoch(), 0, 1,
                                       "boot-recovery-stop", CommandKind::Stop,
                                       {7, 2, 6}, {})).status == Status::Applied,
          "F02 post-boot recovery Stop failed");
  const Command activation = makeCommand(identity, controller.bootEpoch(), 1, 1,
                                         "act-1", CommandKind::Activate,
                                         {7, 2, 4}, Color{1, 2, 3, 0});
  require(controller.apply(activation).status == Status::Applied,
          "F02 first activation was not applied");
  const std::size_t write_count = output.writes;
  require(controller.apply(activation).status == Status::Duplicate &&
              output.writes == write_count,
          "F02 duplicate was not idempotent");
  Command conflicting = activation;
  conflicting.color = Color{9, 8, 7, 0};
  require(controller.apply(conflicting).status == Status::CommandIdConflict,
          "F02 command ID reuse with another payload was accepted");
  std::cout << "PASS F02 duplicate idempotency and command-ID conflict\n";
}

void testGenerationSequenceAndClosedFence() {
  const Identity identity = testIdentity();
  MemoryJournal journal;
  journal.value = provisionedRecord(identity);
  journal.present = true;
  MemoryOutput output(16);
  FencedController controller(identity, testLayout(), journal, output);
  require(controller.boot(), "F03/F04 controller boot failed");
  require(controller.apply(makeCommand(identity, controller.bootEpoch(), 0, 1,
                                       "boot-recovery-stop", CommandKind::Stop,
                                       {7, 2, 6}, {})).status == Status::Applied,
          "F03/F04 post-boot recovery Stop failed");
  require(controller.apply(makeCommand(identity, controller.bootEpoch(), 6, 2,
                                       "newer", CommandKind::Activate,
                                       {7, 2, 4}, Color{1, 0, 0, 0})).status ==
              Status::Applied,
          "F04 sequence 2 did not apply");
  require(controller.apply(makeCommand(identity, controller.bootEpoch(), 6, 1,
                                       "older-sequence", CommandKind::Activate,
                                       {7, 2, 4}, Color{2, 0, 0, 0})).status ==
              Status::StaleSequence,
          "F04 out-of-order sequence was not rejected");
  require(controller.apply(makeCommand(identity, controller.bootEpoch(), 5, 3,
                                       "old-generation", CommandKind::Activate,
                                       {7, 2, 4}, Color{3, 0, 0, 0})).status ==
              Status::StaleGeneration,
          "F03 obsolete generation was not rejected");
  require(controller.apply(makeCommand(identity, controller.bootEpoch(), 7, 1,
                                       "stop-7", CommandKind::Stop,
                                       {7, 2, 4}, {})).status == Status::Applied,
          "F03 fence Stop did not apply");
  require(controller.apply(makeCommand(identity, controller.bootEpoch(), 7, 2,
                                       "same-generation-reactivation",
                                       CommandKind::Activate, {7, 2, 4},
                                       Color{4, 0, 0, 0})).status ==
              Status::GenerationClosed,
          "activation after Stop in a closed generation was accepted");
  std::cout << "PASS F03 generation floor; F04 monotonic sequence and closed Stop generation\n";
}

void testIdentityLayoutAndRangeValidation() {
  const Identity identity = testIdentity();
  MemoryJournal journal;
  journal.value = provisionedRecord(identity);
  journal.present = true;
  MemoryOutput output(16);
  FencedController controller(identity, testLayout(), journal, output);
  require(controller.boot(), "F15 controller boot failed");
  require(controller.apply(makeCommand(identity, controller.bootEpoch(), 0, 1,
                                       "boot-recovery-stop", CommandKind::Stop,
                                       {7, 2, 6}, {})).status == Status::Applied,
          "F15 post-boot recovery Stop failed");

  Command wrong_identity = makeCommand(identity, controller.bootEpoch(), 1, 1,
                                       "wrong-id", CommandKind::Activate,
                                       {7, 2, 4}, Color{1, 0, 0, 0});
  wrong_identity.controller_id = "replacement-controller";
  require(controller.apply(wrong_identity).status == Status::IdentityMismatch,
          "F15 controller identity mismatch was accepted");

  Command wrong_fingerprint = makeCommand(identity, controller.bootEpoch(), 1, 1,
                                          "wrong-layout", CommandKind::Activate,
                                          {7, 2, 4}, Color{1, 0, 0, 0});
  wrong_fingerprint.output_fingerprint = "different-layout";
  require(controller.apply(wrong_fingerprint).status == Status::OutputMismatch,
          "F15 output fingerprint mismatch was accepted");

  require(controller.apply(makeCommand(identity, controller.bootEpoch(), 1, 1,
                                       "outside-owned", CommandKind::Activate,
                                       {7, 6, 8}, Color{1, 0, 0, 0})).status ==
              Status::InvalidRange,
          "unowned gap was accepted");
  require(controller.apply(makeCommand(identity, controller.bootEpoch(), 1, 1,
                                       "out-of-bounds", CommandKind::Activate,
                                       {7, 14, 17}, Color{1, 0, 0, 0})).status ==
              Status::InvalidRange,
          "out-of-bounds range was accepted");

  MemoryJournal mismatch_journal;
  mismatch_journal.value = provisionedRecord(identity);
  mismatch_journal.present = true;
  DeviceLayout mismatched_layout = testLayout();
  mismatched_layout.fingerprint = "different-layout";
  MemoryOutput mismatch_output(16);
  FencedController mismatched(identity, mismatched_layout, mismatch_journal,
                               mismatch_output);
  require(!mismatched.boot(), "mismatched stored output layout did not fail closed");
  std::cout << "PASS F15 stable identity, output fingerprint, owned-range, and bounds checks\n";
}

void testRebootEpochAndLostAcknowledgement() {
  const Identity identity = testIdentity();
  MemoryJournal journal;
  journal.value = provisionedRecord(identity);
  journal.present = true;
  MemoryOutput output(16);
  FencedController first(identity, testLayout(), journal, output);
  require(first.boot(), "F07 first boot failed");
  const std::uint64_t old_epoch = first.bootEpoch();
  require(first.apply(makeCommand(identity, old_epoch, 0, 1, "initial-recovery-stop",
                                  CommandKind::Stop, {7, 2, 6}, {})).status ==
              Status::Applied,
          "F07 initial recovery Stop failed");
  require(first.apply(makeCommand(identity, old_epoch, 1, 1, "active-before-reboot",
                                 CommandKind::Activate, {7, 2, 6},
                                 Color{255, 0, 0, 0})).status == Status::Applied,
          "F07 pre-reboot activation failed");

  FencedController rebooted(identity, testLayout(), journal, output);
  require(rebooted.boot() && rebooted.bootEpoch() == old_epoch + 1,
          "F07 boot epoch did not durably advance");
  require(rebooted.apply(makeCommand(identity, old_epoch, 2, 1, "old-epoch-stop",
                                     CommandKind::Stop, {7, 2, 6}, {})).status ==
              Status::StaleEpoch,
          "F07/F14 prior-epoch request was not rejected after reboot");
  require(rebooted.apply(makeCommand(identity, rebooted.bootEpoch(), 2, 1,
                                     "activation-before-recovery-stop",
                                     CommandKind::Activate, {7, 2, 6},
                                     Color{0, 255, 0, 0})).status ==
              Status::RecoveryRequired,
          "activation was accepted before post-reboot cleanup");
  const Command stop = makeCommand(identity, rebooted.bootEpoch(), 2, 1,
                                   "recovery-stop-2", CommandKind::Stop,
                                   {7, 2, 6}, {});
  require(rebooted.apply(stop).status == Status::Applied,
          "F09 current-epoch recovery Stop failed");
  require(rebooted.apply(stop).status == Status::Duplicate,
          "F09 exact same-epoch retry was not idempotent");
  require(rebooted.apply(makeCommand(identity, rebooted.bootEpoch(), 3, 1,
                                     "activation-after-recovery-stop",
                                     CommandKind::Activate, {7, 2, 6},
                                     Color{0, 255, 0, 0})).status == Status::Applied,
          "activation did not resume after current-epoch cleanup");
  std::cout << "PASS F07 reboot epoch/recovery gate and F09/F14 retry/reconnect handling\n";
}

void testPersistenceFailureAndCorruptionFailClosed() {
  const Identity identity = testIdentity();
  MemoryJournal journal;
  journal.value = provisionedRecord(identity);
  journal.present = true;
  MemoryOutput output(16);
  FencedController controller(identity, testLayout(), journal, output);
  require(controller.boot(), "F08 controller boot failed");
  const auto before = output.pixels;
  journal.fail_next_commit = true;
  const Acknowledgement result = controller.apply(makeCommand(
      identity, controller.bootEpoch(), 9, 1, "fence-with-power-failure",
      CommandKind::Stop, {7, 2, 6}, {}));
  require(result.status == Status::PersistenceFailure && !controller.ready() &&
              output.pixels == before,
          "F08 failed fence persistence changed pixels or left controller ready");

  MemoryJournal corrupt;
  corrupt.value = provisionedRecord(identity);
  corrupt.value.generation_floor = 99;
  corrupt.present = true;
  MemoryOutput corrupt_output(16);
  FencedController corrupt_controller(identity, testLayout(), corrupt,
                                       corrupt_output);
  require(!corrupt_controller.boot(),
          "corrupt persistent fence record did not fail closed");
  std::cout << "PASS F08 persistence failure and corrupted state fail closed (journal model)\n";
}

void testPartialMultiControllerDelivery() {
  const Identity identity_a{"installation-test", "controller-a", "layout-a"};
  const Identity identity_b{"installation-test", "controller-b", "layout-b"};
  const Identity identity_c{"installation-test", "controller-c", "layout-c"};
  MemoryJournal journal_a;
  MemoryJournal journal_b;
  MemoryJournal journal_c;
  journal_a.value = provisionedRecord(identity_a);
  journal_b.value = provisionedRecord(identity_b);
  journal_c.value = provisionedRecord(identity_c);
  journal_a.present = journal_b.present = journal_c.present = true;
  MemoryOutput output_a(16), output_b(16), output_c(16);
  FencedController a(identity_a, DeviceLayout{"layout-a", {{7, 16}}, {{7, 2, 6}}}, journal_a, output_a);
  FencedController b(identity_b, DeviceLayout{"layout-b", {{7, 16}}, {{7, 2, 6}}}, journal_b, output_b);
  FencedController c(identity_c, DeviceLayout{"layout-c", {{7, 16}}, {{7, 2, 6}}}, journal_c, output_c);
  require(a.boot() && b.boot() && c.boot(), "F11 fleet boot failed");
  bool resolved[] = {false, false, false};
  const auto stop_for = [](const Identity& identity, std::uint64_t epoch,
                           const char* id) {
    return makeCommand(identity, epoch, 1, 1, id, CommandKind::Stop,
                       {7, 2, 6}, {});
  };
  resolved[0] = a.apply(stop_for(identity_a, a.bootEpoch(), "a-stop")).status == Status::Applied;
  resolved[2] = c.apply(stop_for(identity_c, c.bootEpoch(), "c-stop")).status == Status::Applied;
  require(!(resolved[0] && resolved[1] && resolved[2]),
          "F11 partial three-controller delivery incorrectly resolved all targets");
  resolved[1] = b.apply(stop_for(identity_b, b.bootEpoch(), "b-stop")).status == Status::Applied;
  require(resolved[0] && resolved[1] && resolved[2],
          "F11 fleet did not resolve after final target acknowledgement");
  std::cout << "PASS F11 release gate requires all three independent target receipts\n";
}

void testApplicationAndGatewayCrashUseHigherFence() {
  const Identity identity = testIdentity();
  MemoryJournal journal;
  journal.value = provisionedRecord(identity);
  journal.present = true;
  MemoryOutput output(16);
  FencedController controller(identity, testLayout(), journal, output);
  require(controller.boot(), "F05/F06 controller boot failed");
  require(controller.apply(makeCommand(identity, controller.bootEpoch(), 0, 1,
                                       "boot-recovery-stop", CommandKind::Stop,
                                       {7, 2, 6}, {})).status == Status::Applied,
          "F05/F06 post-boot recovery Stop failed");
  const Command delivered_before_crash = makeCommand(
      identity, controller.bootEpoch(), 12, 2, "pre-crash-activation",
      CommandKind::Activate, {7, 2, 6}, Color{200, 10, 0, 0});
  require(controller.apply(makeCommand(identity, controller.bootEpoch(), 12, 1,
                                       "first-activation", CommandKind::Activate,
                                       {7, 2, 6}, Color{100, 0, 0, 0})).status ==
              Status::Applied,
          "F05/F06 initial activation failed");
  require(controller.apply(makeCommand(identity, controller.bootEpoch(), 13, 1,
                                       "recovery-fence", CommandKind::Stop,
                                       {7, 2, 6}, {})).status == Status::Applied,
          "F05/F06 recovery fence failed");
  require(controller.apply(delivered_before_crash).status == Status::StaleGeneration,
          "F05/F06 delayed command from crashed sender was not fenced");
  std::cout << "PASS F05/F06 sender and gateway crash recovery by newer device fence\n";
}

}  // namespace

int main() {
  try {
    testDelayedActivationAfterStop();
    testDuplicateAndCommandIdConflict();
    testGenerationSequenceAndClosedFence();
    testIdentityLayoutAndRangeValidation();
    testRebootEpochAndLostAcknowledgement();
    testPersistenceFailureAndCorruptionFailClosed();
    testPartialMultiControllerDelivery();
    testApplicationAndGatewayCrashUseHigherFence();
  } catch (const std::exception& error) {
    std::cerr << "FAIL " << error.what() << '\n';
    return 1;
  }
  std::cout << "All host-model assertions passed; this is not WLED firmware evidence.\n";
  return 0;
}
