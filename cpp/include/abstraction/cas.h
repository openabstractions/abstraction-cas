#pragma once

#include <chrono>
#include <filesystem>
#include <functional>
#include <optional>
#include <stdexcept>
#include <string>
#include <system_error>

namespace abstraction::cas {

using Value = std::optional<std::string>;
using Edit = std::function<std::string(const Value&)>;

// A path as UTF-8 text for a message. path::string() converts to the Windows
// ANSI code page and throws when a name holds a character that page lacks, so an
// error about a file named with an emoji or CJK left as "No mapping for the
// Unicode character" instead of saying what failed.
inline std::string utf8(const std::filesystem::path& p) {
    const auto text = p.u8string();
    return std::string(text.begin(), text.end());
}

struct Moved : std::runtime_error {
    explicit Moved(const std::filesystem::path& p)
        : std::runtime_error("cas: " + utf8(p) + " changed since it was read") {}
};

// A Placement was given its root, a path outside it, or a path in its side directory.
struct OutsideRoot : std::runtime_error {
    OutsideRoot(const std::filesystem::path& p, const std::filesystem::path& root)
        : std::runtime_error("cas: " + utf8(p) + " is not a file under " + utf8(root) + " outside its side directory") {}
};

// A Placement's side directory is on another volume than its root.
struct CrossVolume : std::runtime_error {
    CrossVolume(const std::filesystem::path& root, const std::filesystem::path& side)
        : std::runtime_error("cas: side directory " + utf8(side) + " is on another volume than root " + utf8(root)) {}
};

// Opening a file answered ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION on every
// try until the reader's deadline. code() is the last answer.
struct Refused : std::system_error {
    Refused(const std::filesystem::path& p, int code)
        : std::system_error(code, std::system_category(),
                            "cas: " + utf8(p) + " refused every open until the reader's deadline") {}
};

Value read(const std::filesystem::path& path);
// read, retrying a denied open only until deadline; then throws Refused. A
// writer's replace denies an open for an instant, and a file denied for good is
// denied on every retry.
Value read(const std::filesystem::path& path, std::chrono::steady_clock::time_point deadline);
void write(const std::filesystem::path& path, const Value& base, const std::string& data);
void change(const std::filesystem::path& path, const Edit& edit);

// Removes the staged files a killed writer left beside path, under its lock,
// and returns how many. A staged file is <name>.<unique>.tmp with a unique part
// of letters, digits and underscores.
int sweep(const std::filesystem::path& path);

// Placement keeps the lock file and staged replacements of every file under
// root in side, a directory on the same volume, so root holds only data files.
// For root/a/b the lock is side/a/b.lock, and a replacement is staged as
// side/a/b.<unique>.tmp before it is renamed over root/a/b. Every writer of a
// file must use the same placement.
class Placement {
public:
    // Creates both directories. Throws std::invalid_argument when side is or
    // contains root, and CrossVolume when they are on different volumes.
    Placement(const std::filesystem::path& root, const std::filesystem::path& side);

    Value read(const std::filesystem::path& path) const;
    void write(const std::filesystem::path& path, const Value& base, const std::string& data) const;
    void change(const std::filesystem::path& path, const Edit& edit) const;
    // sweep for the staged files a killed writer left in side for path.
    int sweep(const std::filesystem::path& path) const;

    const std::filesystem::path& root() const { return root_; }
    const std::filesystem::path& side() const { return side_; }

private:
    std::filesystem::path root_, side_;
};

}
