#pragma once

#include <algorithm>
#include <cstdint>
#include <string>
#include <utility>
#include <vector>

namespace task067d {

constexpr std::uint32_t kRecordMagic = 0x574C4631U;
constexpr std::uint32_t kProtocolVersion = 1U;

struct Identity {
  std::string installation_id;
  std::string controller_id;
  std::string output_fingerprint;
};

struct PixelRange {
  std::uint32_t output_id = 0;
  std::uint32_t start = 0;
  std::uint32_t stop = 0;
};

struct OutputDefinition {
  std::uint32_t output_id = 0;
  std::uint32_t length = 0;
};

struct DeviceLayout {
  std::string fingerprint;
  std::vector<OutputDefinition> outputs;
  std::vector<PixelRange> owned_ranges;
};

struct DurableRecord {
  std::uint32_t magic = kRecordMagic;
  std::uint32_t protocol_version = kProtocolVersion;
  std::string installation_id;
  std::string controller_id;
  std::string output_fingerprint;
  std::uint64_t boot_epoch = 0;
  std::uint64_t generation_floor = 0;
  bool generation_closed = true;
  std::uint32_t checksum = 0;
};

struct Color {
  std::uint8_t red = 0;
  std::uint8_t green = 0;
  std::uint8_t blue = 0;
  std::uint8_t white = 0;

  bool operator==(const Color& other) const {
    return red == other.red && green == other.green && blue == other.blue && white == other.white;
  }
};

inline std::uint32_t checksumRecord(const DurableRecord& record) {
  std::uint32_t hash = 2166136261U;
  const auto mixByte = [&hash](std::uint8_t value) {
    hash ^= value;
    hash *= 16777619U;
  };
  const auto mixU32 = [&mixByte](std::uint32_t value) {
    for (unsigned shift = 0; shift < 32; shift += 8) {
      mixByte(static_cast<std::uint8_t>(value >> shift));
    }
  };
  const auto mixU64 = [&mixByte](std::uint64_t value) {
    for (unsigned shift = 0; shift < 64; shift += 8) {
      mixByte(static_cast<std::uint8_t>(value >> shift));
    }
  };
  const auto mixString = [&mixByte, &mixU32](const std::string& value) {
    mixU32(static_cast<std::uint32_t>(value.size()));
    for (unsigned char byte : value) mixByte(byte);
  };

  mixU32(record.magic);
  mixU32(record.protocol_version);
  mixString(record.installation_id);
  mixString(record.controller_id);
  mixString(record.output_fingerprint);
  mixU64(record.boot_epoch);
  mixU64(record.generation_floor);
  mixByte(record.generation_closed ? 1U : 0U);
  return hash;
}

inline DurableRecord provisionedRecord(const Identity& identity, std::uint64_t boot_epoch = 0) {
  DurableRecord record;
  record.installation_id = identity.installation_id;
  record.controller_id = identity.controller_id;
  record.output_fingerprint = identity.output_fingerprint;
  record.boot_epoch = boot_epoch;
  record.generation_floor = 0;
  record.generation_closed = true;
  record.checksum = checksumRecord(record);
  return record;
}

class Journal {
 public:
  virtual ~Journal() = default;
  virtual bool load(DurableRecord& record) const = 0;

  // Implementations must publish each record atomically or leave the previous
  // valid record readable. This prototype deliberately has no flash backend.
  virtual bool commit(const DurableRecord& record) = 0;
};

class PixelOutput {
 public:
  virtual ~PixelOutput() = default;
  virtual bool writeRange(std::uint32_t output_id, std::uint32_t start,
                          std::uint32_t stop, Color color) = 0;
};

enum class CommandKind { Activate, Stop };

enum class Status {
  Applied,
  Duplicate,
  NotReady,
  ProtocolMismatch,
  IdentityMismatch,
  OutputMismatch,
  StaleEpoch,
  StaleGeneration,
  RecoveryRequired,
  GenerationClosed,
  StaleSequence,
  CommandIdConflict,
  InvalidRange,
  PersistenceFailure,
  OutputFailure,
};

struct Command {
  std::uint32_t protocol_version = kProtocolVersion;
  std::string installation_id;
  std::string controller_id;
  std::string output_fingerprint;
  std::uint64_t boot_epoch = 0;
  std::uint64_t generation = 0;
  std::uint64_t sequence = 0;
  std::string command_id;
  CommandKind kind = CommandKind::Activate;
  std::vector<PixelRange> ranges;
  Color color;
};

struct Acknowledgement {
  Status status = Status::NotReady;
  std::string installation_id;
  std::string controller_id;
  std::string output_fingerprint;
  std::uint64_t boot_epoch = 0;
  std::uint64_t generation = 0;
  std::uint64_t sequence = 0;
  std::string command_id;
};

class FencedController {
 public:
  FencedController(Identity identity, DeviceLayout layout, Journal& journal,
                   PixelOutput& output)
      : identity_(std::move(identity)),
        layout_(std::move(layout)),
        journal_(journal),
        output_(output) {}

  bool boot() {
    ready_ = false;
    DurableRecord loaded;
    if (!journal_.load(loaded) || loaded.magic != kRecordMagic ||
        loaded.protocol_version != kProtocolVersion ||
        loaded.checksum != checksumRecord(loaded) ||
        loaded.installation_id != identity_.installation_id ||
        loaded.controller_id != identity_.controller_id ||
        loaded.output_fingerprint != identity_.output_fingerprint ||
        identity_.output_fingerprint != layout_.fingerprint ||
        loaded.boot_epoch == UINT64_MAX) {
      return false;
    }

    ++loaded.boot_epoch;
    loaded.checksum = checksumRecord(loaded);
    if (!journal_.commit(loaded)) return false;

    durable_ = std::move(loaded);
    last_sequence_ = 0;
    outcomes_.clear();
    recovery_required_ = true;
    ready_ = true;
    return true;
  }

  Acknowledgement apply(const Command& command) {
    Acknowledgement ack{Status::NotReady, identity_.installation_id,
                        identity_.controller_id, identity_.output_fingerprint,
                        durable_.boot_epoch, command.generation,
                        command.sequence, command.command_id};
    if (!ready_) return ack;
    if (command.protocol_version != kProtocolVersion) {
      ack.status = Status::ProtocolMismatch;
      return ack;
    }
    if (command.installation_id != identity_.installation_id ||
        command.controller_id != identity_.controller_id) {
      ack.status = Status::IdentityMismatch;
      return ack;
    }
    if (command.output_fingerprint != identity_.output_fingerprint) {
      ack.status = Status::OutputMismatch;
      return ack;
    }
    if (command.boot_epoch != durable_.boot_epoch) {
      ack.status = Status::StaleEpoch;
      return ack;
    }

    const Command* prior = findOutcome(command.command_id);
    if (prior && !sameCommand(*prior, command)) {
      ack.status = Status::CommandIdConflict;
      return ack;
    }
    if (command.generation < durable_.generation_floor) {
      ack.status = Status::StaleGeneration;
      return ack;
    }
    if (command.kind == CommandKind::Activate && recovery_required_) {
      ack.status = Status::RecoveryRequired;
      return ack;
    }
    if (prior) {
      ack.status = Status::Duplicate;
      return ack;
    }
    if (command.command_id.empty() || command.sequence == 0 ||
        command.ranges.empty() ||
        (command.kind == CommandKind::Stop && !(command.color == Color{}))) {
      ack.status = Status::InvalidRange;
      return ack;
    }
    if (command.kind == CommandKind::Activate &&
        command.generation == durable_.generation_floor &&
        durable_.generation_closed) {
      ack.status = Status::GenerationClosed;
      return ack;
    }

    const std::uint64_t sequence_floor =
        command.generation == durable_.generation_floor ? last_sequence_ : 0;
    if (command.sequence <= sequence_floor) {
      ack.status = Status::StaleSequence;
      return ack;
    }
    for (const PixelRange& range : command.ranges) {
      if (!rangeIsOwnedAndValid(range)) {
        ack.status = Status::InvalidRange;
        return ack;
      }
    }

    DurableRecord next = durable_;
    const bool advances_generation = command.generation > durable_.generation_floor;
    const bool closes_current_generation =
        command.kind == CommandKind::Stop && !durable_.generation_closed;
    if (advances_generation || closes_current_generation) {
      next.generation_floor = command.generation;
      next.generation_closed = command.kind == CommandKind::Stop;
      next.checksum = checksumRecord(next);
      if (!journal_.commit(next)) {
        ready_ = false;
        ack.status = Status::PersistenceFailure;
        return ack;
      }
      durable_ = std::move(next);
      if (advances_generation) last_sequence_ = 0;
    }

    for (const PixelRange& range : command.ranges) {
      if (!output_.writeRange(range.output_id, range.start, range.stop,
                              command.color)) {
        ready_ = false;
        ack.status = Status::OutputFailure;
        return ack;
      }
    }

    last_sequence_ = command.sequence;
    outcomes_.push_back(command);
    if (command.kind == CommandKind::Stop) recovery_required_ = false;
    ack.status = Status::Applied;
    ack.boot_epoch = durable_.boot_epoch;
    return ack;
  }

  bool ready() const { return ready_; }
  std::uint64_t bootEpoch() const { return durable_.boot_epoch; }
  std::uint64_t generationFloor() const { return durable_.generation_floor; }

 private:
  bool rangeIsOwnedAndValid(const PixelRange& range) const {
    if (range.start >= range.stop) return false;
    const auto output = std::find_if(
        layout_.outputs.begin(), layout_.outputs.end(),
        [&range](const OutputDefinition& item) {
          return item.output_id == range.output_id;
        });
    if (output == layout_.outputs.end() || range.stop > output->length) return false;
    return std::any_of(layout_.owned_ranges.begin(), layout_.owned_ranges.end(),
                       [&range](const PixelRange& owned) {
                         return owned.output_id == range.output_id &&
                                range.start >= owned.start &&
                                range.stop <= owned.stop;
                       });
  }

  const Command* findOutcome(const std::string& command_id) const {
    const auto found = std::find_if(outcomes_.begin(), outcomes_.end(),
                                    [&command_id](const Command& item) {
                                      return item.command_id == command_id;
                                    });
    return found == outcomes_.end() ? nullptr : &*found;
  }

  static bool sameCommand(const Command& left, const Command& right) {
    if (left.protocol_version != right.protocol_version ||
        left.installation_id != right.installation_id ||
        left.controller_id != right.controller_id ||
        left.output_fingerprint != right.output_fingerprint ||
        left.boot_epoch != right.boot_epoch ||
        left.generation != right.generation ||
        left.sequence != right.sequence || left.command_id != right.command_id ||
        left.kind != right.kind || !(left.color == right.color) ||
        left.ranges.size() != right.ranges.size()) {
      return false;
    }
    for (std::size_t index = 0; index < left.ranges.size(); ++index) {
      const PixelRange& a = left.ranges[index];
      const PixelRange& b = right.ranges[index];
      if (a.output_id != b.output_id || a.start != b.start || a.stop != b.stop) {
        return false;
      }
    }
    return true;
  }

  Identity identity_;
  DeviceLayout layout_;
  Journal& journal_;
  PixelOutput& output_;
  DurableRecord durable_;
  std::uint64_t last_sequence_ = 0;
  std::vector<Command> outcomes_;
  bool recovery_required_ = true;
  bool ready_ = false;
};

}  // namespace task067d
