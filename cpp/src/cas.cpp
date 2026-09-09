#include <abstraction/cas.h>

#include <cstring>
#include <random>
#include <system_error>
#include <vector>

namespace fs = std::filesystem;
using abstraction::cas::Value;

namespace {

fs::path fresh(const fs::path& path) {
    static std::random_device seed;
    fs::path tmp = path;
    return tmp += "." + std::to_string(seed()) + ".tmp";
}

}

#ifdef _WIN32

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
    throw std::system_error(int(e), std::system_category(), std::string(op) + " " + p.string());
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

Value read_file(const fs::path& p) {
    Handle f = open_shared(p, GENERIC_READ);
    if (!f.open()) {
        Err e = GetLastError();
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

fs::path stage(const fs::path& p, const std::string& data) {
    fs::path tmp = fresh(p);
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

}

#else

#include <fcntl.h>
#include <sys/file.h>
#include <unistd.h>
#include <cerrno>

namespace {

using Err = int;

[[noreturn]] void fail(const char* op, const fs::path& p, Err e) {
    throw std::system_error(e, std::generic_category(), std::string(op) + " " + p.string());
}

struct Handle {
    int fd;
    explicit Handle(int fd) : fd(fd) {}
    Handle(const Handle&) = delete;
    ~Handle() { if (fd >= 0) close(fd); }
    bool open() const { return fd >= 0; }
};

Value read_file(const fs::path& p) {
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

fs::path stage(const fs::path& p, const std::string& data) {
    fs::path tmp = fresh(p);
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

}

#endif

namespace {

Lock lock(const fs::path& path) {
    if (fs::path dir = path.parent_path(); !dir.empty()) fs::create_directories(dir);
    fs::path p = path;
    return Lock(p += ".lock");
}

void replace(const fs::path& path, const Value& base, const std::string& data) {
    if (read_file(path) != base) throw abstraction::cas::Moved(path);
    fs::path tmp = stage(path, data);
    Err e = 0;
    for (int tries = 0; tries < 1000; ++tries) {
        e = rename_over(tmp, path);
        if (!transient(e)) break;
    }
    if (e) {
        std::error_code ignored;
        fs::remove(tmp, ignored);
        fail("rename", path, e);
    }
}

}

namespace abstraction::cas {

Value read(const fs::path& path) { return read_file(path); }

void write(const fs::path& path, const Value& base, const std::string& data) {
    Lock held = lock(path);
    replace(path, base, data);
}

void change(const fs::path& path, const Edit& edit) {
    Lock held = lock(path);
    Value cur = read_file(path);
    std::string next = edit(cur);
    if (cur != next) replace(path, cur, next);
}

}
