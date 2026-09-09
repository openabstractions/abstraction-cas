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

Value read(const std::filesystem::path& path);
void write(const std::filesystem::path& path, const Value& base, const std::string& data);
void change(const std::filesystem::path& path, const Edit& edit);

}
