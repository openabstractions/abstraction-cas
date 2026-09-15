#pragma once

#include <filesystem>
#include <functional>
#include <optional>
#include <stdexcept>
#include <string>

namespace abstraction::cas {

using Value = std::optional<std::string>;
using Edit = std::function<std::string(const Value&)>;

struct Moved : std::runtime_error {
    explicit Moved(const std::filesystem::path& p)
        : std::runtime_error("cas: " + p.string() + " changed since it was read") {}
};

// A Placement was given its root, a path outside it, or a path in its side directory.
struct OutsideRoot : std::runtime_error {
    OutsideRoot(const std::filesystem::path& p, const std::filesystem::path& root)
        : std::runtime_error("cas: " + p.string() + " is not a file under " + root.string() + " outside its side directory") {}
};

// A Placement's side directory is on another volume than its root.
struct CrossVolume : std::runtime_error {
    CrossVolume(const std::filesystem::path& root, const std::filesystem::path& side)
        : std::runtime_error("cas: side directory " + side.string() + " is on another volume than root " + root.string()) {}
};

Value read(const std::filesystem::path& path);
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
