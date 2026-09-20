#include <abstraction/cas.h>

#include <cstring>
#include <random>
#include <system_error>
#include <vector>

namespace fs = std::filesystem;
using abstraction::cas::Value;

// Test hooks. This layer's own tests set them; applications never do.
namespace abstraction::cas::testing {
void (*after_stage)(const std::filesystem::path& staged) = nullptr;
void (*after_directory_sync)(const std::filesystem::path& directory) = nullptr;
}

namespace {

fs::path fresh(const fs::path& dir, const fs::path& name) {
    static std::random_device seed;
    fs::path tmp = dir / name;
    return tmp += "." + std::to_string(seed()) + ".tmp";
}

// The files one write uses: the data file, its lock and the directory its
// replacement is staged in.
struct Placed {
    fs::path target, lock, stage;
};

// The default placement: the lock and the staged file sit beside the data file.
Placed beside(const fs::path& path) {
    fs::path lock = path;
    lock += ".lock";
    return {path, lock, path.parent_path()};
}

}

#ifdef _WIN32

// FileRenameInfoEx is a Windows 10 declaration. MinGW's headers default to an
// older _WIN32_WINNT and hide it; rename_over falls back to MoveFileExW where a
// running system refuses it. Workflows compile this file by its path with no
// build file, so the target is set here, where every build sees it.
#if !defined(_WIN32_WINNT) || _WIN32_WINNT < 0x0A00
#undef _WIN32_WINNT
#define _WIN32_WINNT 0x0A00
#endif
#define WIN32_LEAN_AND_MEAN
#include <windows.h>

#ifndef FILE_RENAME_FLAG_POSIX_SEMANTICS
#define FILE_RENAME_FLAG_REPLACE_IF_EXISTS 1
#define FILE_RENAME_FLAG_POSIX_SEMANTICS 2
#endif

namespace {

using Err = DWORD;
const DWORD share_all = FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE;

[[noreturn]] void fail(const char* op, const fs::path& p, Err e) {
    throw std::system_error(int(e), std::system_category(), std::string(op) + " " + abstraction::cas::utf8(p));
}

struct Handle {
    HANDLE h;
    explicit Handle(HANDLE h) : h(h) {}
    Handle(const Handle&) = delete;
    ~Handle() { if (h != INVALID_HANDLE_VALUE) CloseHandle(h); }
    bool open() const { return h != INVALID_HANDLE_VALUE; }
};

Handle open_shared(const fs::path& p, DWORD access, DWORD disposition = OPEN_EXISTING) {
    return Handle(CreateFileW(p.c_str(), access, share_all, nullptr, disposition, FILE_ATTRIBUTE_NORMAL, nullptr));
}

bool transient(Err e);

// A read races every writer's rename. While a replaced file is being deleted,
// or another process holds it without FILE_SHARE_DELETE for an instant, opening
// its name answers ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION. The writer
// already retries its rename on those; the reader did not, and a spinning C++
// reader in mixed.py died on one about one run in three on a loaded Windows host.
// A reader's deadline, when given, ends the retry early with Refused.
HANDLE open_for_read(const fs::path& p, Err& e, const std::chrono::steady_clock::time_point* deadline) {
    for (int tries = 0;; ++tries) {
        HANDLE h = CreateFileW(p.c_str(), GENERIC_READ, share_all, nullptr, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, nullptr);
        if (h != INVALID_HANDLE_VALUE) return h;
        e = GetLastError();
        if (!transient(e) || tries >= 2000) return h;
        if (deadline && std::chrono::steady_clock::now() >= *deadline) throw abstraction::cas::Refused(p, int(e));
        if (tries >= 50) Sleep(1);
    }
}

Value read_file(const fs::path& p, const std::chrono::steady_clock::time_point* deadline = nullptr) {
    Err e = 0;
    Handle f(open_for_read(p, e, deadline));
    if (!f.open()) {
        if (e == ERROR_FILE_NOT_FOUND || e == ERROR_PATH_NOT_FOUND) return std::nullopt;
        fail("open", p, e);
    }
    std::string out;
    char buf[1 << 16];
    for (DWORD n;;) {
        if (!ReadFile(f.h, buf, sizeof buf, &n, nullptr)) fail("read", p, GetLastError());
        if (n == 0) return out;
        out.append(buf, n);
    }
}

struct Lock {
    Handle f;
    explicit Lock(const fs::path& p) : f(open_shared(p, GENERIC_READ | GENERIC_WRITE, OPEN_ALWAYS)) {
        if (!f.open()) fail("open", p, GetLastError());
        OVERLAPPED ov{};
        if (!LockFileEx(f.h, LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &ov)) fail("lock", p, GetLastError());
    }
};

fs::path stage(const Placed& p, const std::string& data) {
    fs::path tmp = fresh(p.stage, p.target.filename());
    Handle f(CreateFileW(tmp.c_str(), GENERIC_WRITE, 0, nullptr, CREATE_NEW, FILE_ATTRIBUTE_NORMAL, nullptr));
    if (!f.open()) fail("create", tmp, GetLastError());
    DWORD n = 0;
    if (!WriteFile(f.h, data.data(), DWORD(data.size()), &n, nullptr) || n != data.size() || !FlushFileBuffers(f.h)) {
        Err e = GetLastError();
        DeleteFileW(tmp.c_str());
        fail("write", tmp, e);
    }
    return tmp;
}

bool transient(Err e) { return e == ERROR_ACCESS_DENIED || e == ERROR_SHARING_VIOLATION; }

Err rename_over(const fs::path& tmp, const fs::path& to) {
    Err e = 0;
    {
        Handle f = open_shared(tmp, DELETE);
        if (!f.open()) return GetLastError();
        std::wstring name = fs::absolute(to).wstring();
        std::vector<char> buf(sizeof(FILE_RENAME_INFO) + name.size() * sizeof(wchar_t));
        auto* info = reinterpret_cast<FILE_RENAME_INFO*>(buf.data());
        info->Flags = FILE_RENAME_FLAG_REPLACE_IF_EXISTS | FILE_RENAME_FLAG_POSIX_SEMANTICS;
        info->RootDirectory = nullptr;
        info->FileNameLength = DWORD(name.size() * sizeof(wchar_t));
        std::memcpy(info->FileName, name.c_str(), (name.size() + 1) * sizeof(wchar_t));
        if (SetFileInformationByHandle(f.h, FileRenameInfoEx, info, DWORD(buf.size()))) return 0;
        e = GetLastError();
    }
    if (e != ERROR_INVALID_PARAMETER && e != ERROR_NOT_SUPPORTED) return e;
    return MoveFileExW(tmp.c_str(), to.c_str(), MOVEFILE_REPLACE_EXISTING) ? 0 : GetLastError();
}

unsigned long long volume_of(const fs::path& dir) {
    Handle f(CreateFileW(dir.c_str(), FILE_READ_ATTRIBUTES, share_all, nullptr, OPEN_EXISTING,
                         FILE_FLAG_BACKUP_SEMANTICS, nullptr));
    if (!f.open()) fail("open", dir, GetLastError());
    BY_HANDLE_FILE_INFORMATION info;
    if (!GetFileInformationByHandle(f.h, &info)) fail("volume", dir, GetLastError());
    return info.dwVolumeSerialNumber;
}

// C++ flushes the directory on POSIX only.
void sync_dir(const fs::path&) {}

}

#else

#include <fcntl.h>
#include <sys/file.h>
#include <sys/stat.h>
#include <unistd.h>
#include <cerrno>

namespace {

using Err = int;

[[noreturn]] void fail(const char* op, const fs::path& p, Err e) {
    throw std::system_error(e, std::generic_category(), std::string(op) + " " + abstraction::cas::utf8(p));
}

struct Handle {
    int fd;
    explicit Handle(int fd) : fd(fd) {}
    Handle(const Handle&) = delete;
    ~Handle() { if (fd >= 0) close(fd); }
    bool open() const { return fd >= 0; }
};

// POSIX has no transient denial to retry, so the deadline changes nothing.
Value read_file(const fs::path& p, const std::chrono::steady_clock::time_point* = nullptr) {
    Handle f(::open(p.c_str(), O_RDONLY | O_CLOEXEC));
    if (!f.open()) {
        if (errno == ENOENT) return std::nullopt;
        fail("open", p, errno);
    }
    std::string out;
    char buf[1 << 16];
    for (;;) {
        ssize_t n = ::read(f.fd, buf, sizeof buf);
        if (n < 0) fail("read", p, errno);
        if (n == 0) return out;
        out.append(buf, size_t(n));
    }
}

struct Lock {
    Handle f;
    explicit Lock(const fs::path& p) : f(::open(p.c_str(), O_CREAT | O_RDWR | O_CLOEXEC, 0600)) {
        if (!f.open()) fail("open", p, errno);
        if (flock(f.fd, LOCK_EX) != 0) fail("lock", p, errno);
    }
};

fs::path stage(const Placed& p, const std::string& data) {
    fs::path tmp = fresh(p.stage, p.target.filename());
    Handle f(::open(tmp.c_str(), O_WRONLY | O_CREAT | O_EXCL | O_CLOEXEC, 0600));
    if (!f.open()) fail("create", tmp, errno);
    for (size_t done = 0; done < data.size();) {
        ssize_t n = ::write(f.fd, data.data() + done, data.size() - done);
        if (n < 0) {
            Err e = errno;
            unlink(tmp.c_str());
            fail("write", tmp, e);
        }
        done += size_t(n);
    }
    if (fsync(f.fd) != 0) {
        Err e = errno;
        unlink(tmp.c_str());
        fail("sync", tmp, e);
    }
    return tmp;
}

bool transient(Err) { return false; }

Err rename_over(const fs::path& tmp, const fs::path& to) {
    return ::rename(tmp.c_str(), to.c_str()) == 0 ? 0 : errno;
}

unsigned long long volume_of(const fs::path& dir) {
    struct stat st;
    if (::stat(dir.c_str(), &st) != 0) fail("stat", dir, errno);
    return (unsigned long long)st.st_dev;
}

// Flushes a directory after a rename in it, so the new entry survives a power
// cut. A file system that cannot sync a directory answers EINVAL or ENOTSUP, and
// the write stands without that guarantee.
void sync_dir(const fs::path& dir) {
    const fs::path where = dir.empty() ? fs::path(".") : dir;
    Handle d(::open(where.c_str(), O_RDONLY | O_CLOEXEC | O_DIRECTORY));
    if (!d.open()) fail("open", where, errno);
    if (fsync(d.fd) != 0 && errno != EINVAL && errno != ENOTSUP) fail("sync", where, errno);
}

}

#endif

namespace {

Lock lock(const Placed& p) {
    if (fs::path dir = p.target.parent_path(); !dir.empty()) fs::create_directories(dir);
    if (fs::path dir = p.lock.parent_path(); !dir.empty()) fs::create_directories(dir);
    return Lock(p.lock);
}

void replace(const Placed& p, const Value& base, const std::string& data) {
    if (read_file(p.target) != base) throw abstraction::cas::Moved(p.target);
    fs::path tmp = stage(p, data);
    if (abstraction::cas::testing::after_stage) abstraction::cas::testing::after_stage(tmp);
    Err e = 0;
    for (int tries = 0; tries < 1000; ++tries) {
        e = rename_over(tmp, p.target);
        if (!transient(e)) break;
    }
    if (e) {
        std::error_code ignored;
        fs::remove(tmp, ignored);
        fail("rename", p.target, e);
    }
    fs::path dir = p.target.parent_path();
    sync_dir(dir);
    if (abstraction::cas::testing::after_directory_sync) abstraction::cas::testing::after_directory_sync(dir);
}

using Native = fs::path::string_type;

// Whether name is <prefix><unique>.tmp with a unique part of letters, digits and
// underscores: the names this writer, Go's CreateTemp and Python's mkstemp give
// a staged replacement. A dot in the unique part belongs to another file's name.
bool staged_name(const Native& name, const Native& prefix) {
    const Native suffix = fs::path(".tmp").native();
    if (name.size() <= prefix.size() + suffix.size() || name.compare(0, prefix.size(), prefix) != 0 ||
        name.compare(name.size() - suffix.size(), suffix.size(), suffix) != 0)
        return false;
    for (size_t i = prefix.size(); i < name.size() - suffix.size(); ++i) {
        auto c = name[i];
        if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_')) return false;
    }
    return true;
}

int sweep_placed(const Placed& p) {
    Lock held = lock(p);
    Native prefix = p.target.filename().native();
    prefix += fs::path(".").native();
    const fs::path dir = p.stage.empty() ? fs::path(".") : p.stage;
    int gone = 0;
    std::error_code ec;
    for (const auto& entry : fs::directory_iterator(dir, ec)) {
        std::error_code type;
        if (!entry.is_regular_file(type) || !staged_name(entry.path().filename().native(), prefix)) continue;
        std::error_code removed;
        if (fs::remove(entry.path(), removed)) ++gone;
    }
    if (ec) throw std::system_error(ec, "sweep " + abstraction::cas::utf8(dir));
    return gone;
}

void change_placed(const Placed& p, const abstraction::cas::Edit& edit) {
    Lock held = lock(p);
    Value cur = read_file(p.target);
    std::string next = edit(cur);
    if (cur != next) replace(p, cur, next);
}

fs::path clean(const fs::path& p) {
    fs::path out = fs::absolute(p).lexically_normal();
    if (!out.has_filename() && out.has_relative_path()) out = out.parent_path();
    return out;
}

// child relative to parent, or nothing when child is neither parent nor under it.
std::optional<fs::path> relative_within(const fs::path& parent, const fs::path& child) {
    fs::path rel = child.lexically_relative(parent);
    if (rel.empty()) return std::nullopt;
    if (auto first = rel.begin(); first != rel.end() && *first == "..") return std::nullopt;
    return rel;
}

Placed place(const fs::path& root, const fs::path& side, const fs::path& path) {
    fs::path target = clean(path);
    std::optional<fs::path> rel = relative_within(root, target);
    if (!rel || *rel == "." || relative_within(side, target)) throw abstraction::cas::OutsideRoot(target, root);
    fs::path mirror = side / *rel;
    fs::path lock = mirror;
    lock += ".lock";
    return {target, lock, mirror.parent_path()};
}

}

namespace abstraction::cas {

Value read(const fs::path& path) { return read_file(path); }

Value read(const fs::path& path, std::chrono::steady_clock::time_point deadline) { return read_file(path, &deadline); }

void write(const fs::path& path, const Value& base, const std::string& data) {
    Placed p = beside(path);
    Lock held = lock(p);
    replace(p, base, data);
}

void change(const fs::path& path, const Edit& edit) { change_placed(beside(path), edit); }

int sweep(const fs::path& path) { return sweep_placed(beside(path)); }

Placement::Placement(const fs::path& root, const fs::path& side) : root_(clean(root)), side_(clean(side)) {
    if (relative_within(side_, root_))
        throw std::invalid_argument("cas: side directory " + utf8(side_) + " contains root " + utf8(root_));
    fs::create_directories(root_);
    fs::create_directories(side_);
    if (volume_of(root_) != volume_of(side_)) throw CrossVolume(root_, side_);
}

Value Placement::read(const fs::path& path) const { return read_file(place(root_, side_, path).target); }

void Placement::write(const fs::path& path, const Value& base, const std::string& data) const {
    Placed p = place(root_, side_, path);
    Lock held = lock(p);
    replace(p, base, data);
}

void Placement::change(const fs::path& path, const Edit& edit) const {
    change_placed(place(root_, side_, path), edit);
}

int Placement::sweep(const fs::path& path) const { return sweep_placed(place(root_, side_, path)); }

}
