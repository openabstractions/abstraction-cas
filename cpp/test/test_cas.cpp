#define _CRT_SECURE_NO_WARNINGS
#include <abstraction/cas.h>

#include <algorithm>
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <fstream>
#include <optional>
#include <filesystem>
#include <iterator>
#include <mutex>
#include <random>
#include <string>
#include <thread>
#include <vector>

#ifdef _WIN32
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <fcntl.h>
#include <io.h>
#else
#include <spawn.h>
#include <csignal>
#include <sys/wait.h>
#include <unistd.h>
#ifdef __APPLE__
#include <mach-o/dyld.h>
#include <cstdint>
#endif
extern char** environ;
#endif

namespace fs = std::filesystem;
using namespace abstraction::cas;

// Defined in cas.cpp for this test only.
namespace abstraction::cas::testing {
extern void (*after_stage)(const std::filesystem::path& staged);
extern void (*after_directory_sync)(const std::filesystem::path& directory);
}

static int failures = 0;

static void check(bool cond, const std::string& what, const std::string& why = "") {
    std::printf("  %s  %s\n", cond ? "PASS" : "FAIL", what.c_str());
    if (!cond) {
        std::printf("        %s\n", why.c_str());
        ++failures;
    }
}

static int counter(const Value& cur) {
    if (!cur) return 0;
    int a = -1, b = -2;
    if (std::sscanf(cur->c_str(), "%d %d", &a, &b) != 2 || a != b) {
        std::fprintf(stderr, "torn read: \"%s\"\n", cur->c_str());
        std::exit(3);
    }
    return a;
}

static std::string increment(const Value& cur) {
    int n = counter(cur) + 1;
    return std::to_string(n) + " " + std::to_string(n);
}

static fs::path env_path(const char* name) {
#ifdef _WIN32
    std::wstring wide(name, name + std::strlen(name));
    const wchar_t* p = _wgetenv(wide.c_str());
#else
    const char* p = std::getenv(name);
#endif
    return p ? fs::path(p) : fs::path();
}

static void set_env(const char* name, const fs::path& value) {
#ifdef _WIN32
    std::wstring wide(name, name + std::strlen(name));
    SetEnvironmentVariableW(wide.c_str(), value.empty() ? nullptr : value.c_str());
#else
    if (value.empty()) unsetenv(name);
    else setenv(name, value.c_str(), 1);
#endif
}

static int role(const std::string& name, const fs::path& path, int target) {
    try {
        std::optional<Placement> placement;
        if (fs::path side = env_path("CAS_SIDE"); !side.empty()) placement.emplace(env_path("CAS_ROOT"), side);
        if (name == "writer") {
            for (int i = 0; i < target; ++i) {
                if (placement) placement->change(path, increment);
                else change(path, increment);
            }
        } else if (name == "reader") {
            while (counter(placement ? placement->read(path) : read(path)) != target) {
            }
        } else if (name == "killed") {
            // Stage one change, leave the marker, and wait to be killed before the rename.
            testing::after_stage = [](const fs::path&) {
                std::ofstream(env_path("CAS_MARKER")) << "staged";
                std::this_thread::sleep_for(std::chrono::seconds(120));
            };
            if (placement) placement->change(path, increment);
            else change(path, increment);
        }
        return 0;
    } catch (const std::exception& e) {
        std::fprintf(stderr, "%s: %s\n", name.c_str(), e.what());
        return 2;
    }
}

static fs::path fresh_dir() {
    std::random_device seed;
    fs::path d = fs::temp_directory_path() / ("abstraction-cas-" + std::to_string(seed()));
    fs::create_directories(d);
    return d;
}

static long entries(const fs::path& dir) {
    return long(std::distance(fs::directory_iterator(dir), fs::directory_iterator()));
}

static bool moved(const fs::path& p, const Value& base, const std::string& data) {
    try {
        write(p, base, data);
        return false;
    } catch (const Moved&) {
        return true;
    }
}

static void stale_write_is_refused() {
    fs::path dir = fresh_dir(), p = dir / "v";
    check(moved(p, "x", "1"), "base on a missing file is refused");
    write(p, std::nullopt, "1");
    check(moved(p, std::nullopt, "2"), "no base on an existing file is refused");
    check(moved(p, "0", "2"), "stale base is refused");
    check(read(p) == "1", "a refused write leaves the file alone");
    write(p, "1", "2");
    check(read(p) == "2", "a current base is accepted");
    check(entries(dir) == 2, "refused writes leave nothing behind", std::to_string(entries(dir)));
    fs::remove_all(dir);
}

static void missing_reads_as_nullopt() {
    fs::path dir = fresh_dir(), p = dir / "v";
    check(!read(p), "missing reads as nullopt");
    write(p, std::nullopt, "");
    Value v = read(p);
    check(v && v->empty(), "an empty file reads as empty, not missing");
    fs::remove_all(dir);
}

static void missing_to_empty_is_a_change() {
    fs::path dir = fresh_dir(), p = dir / "v";
    change(p, [](const Value&) { return std::string(); });
    Value v = read(p);
    check(v && v->empty(), "an edit from missing to empty writes an empty file");
    fs::remove_all(dir);
}

static void no_lost_update_in_process() {
    fs::path dir = fresh_dir(), p = dir / "n";
    const int writers = 8, each = 50;
    std::vector<std::thread> ts;
    for (int w = 0; w < writers; ++w) {
        ts.emplace_back([&] {
            for (int i = 0; i < each; ++i) change(p, increment);
        });
    }
    for (auto& t : ts) t.join();
    int got = counter(read(p));
    check(got == writers * each, "no lost update across threads",
          std::to_string(got) + " of " + std::to_string(writers * each) + " survived");
    fs::remove_all(dir);
}

#ifdef _WIN32
using Child = HANDLE;

static Child spawn(const std::string& role, int n) {
    SetEnvironmentVariableA("CAS_ROLE", role.c_str());
    SetEnvironmentVariableA("CAS_N", std::to_string(n).c_str());
    wchar_t exe[MAX_PATH];
    GetModuleFileNameW(nullptr, exe, MAX_PATH);
    STARTUPINFOW si{};
    si.cb = sizeof si;
    PROCESS_INFORMATION pi{};
    if (!CreateProcessW(exe, nullptr, nullptr, nullptr, FALSE, 0, nullptr, nullptr, &si, &pi)) {
        std::fprintf(stderr, "CreateProcess: %lu\n", GetLastError());
        std::exit(4);
    }
    CloseHandle(pi.hThread);
    return pi.hProcess;
}

static int join(Child c) {
    WaitForSingleObject(c, INFINITE);
    DWORD code = 1;
    GetExitCodeProcess(c, &code);
    CloseHandle(c);
    return int(code);
}

static void kill(Child c) { TerminateProcess(c, 1); }
#else
using Child = pid_t;

static std::string self_path() {
#ifdef __APPLE__
    std::uint32_t size = 0;
    _NSGetExecutablePath(nullptr, &size);
    std::string buf(size, '\0');
    if (_NSGetExecutablePath(buf.data(), &size) != 0) return {};
    buf.resize(std::string(buf.c_str()).size());
    std::error_code ec;
    const fs::path real = fs::canonical(buf, ec);
    return ec ? buf : real.string();
#else
    return fs::read_symlink("/proc/self/exe").string();
#endif
}

static Child spawn(const std::string& role, int n) {
    setenv("CAS_ROLE", role.c_str(), 1);
    setenv("CAS_N", std::to_string(n).c_str(), 1);
    std::string exe = self_path();
    char* argv[] = {exe.data(), nullptr};
    pid_t pid;
    if (posix_spawn(&pid, exe.c_str(), nullptr, nullptr, argv, environ) != 0) {
        std::perror("posix_spawn");
        std::exit(4);
    }
    return pid;
}

static int join(Child c) {
    int status = 0;
    waitpid(c, &status, 0);
    return WIFEXITED(status) ? WEXITSTATUS(status) : -1;
}

static void kill(Child c) { ::kill(c, SIGKILL); }
#endif

static void no_lost_update_across_processes() {
    fs::path dir = fresh_dir(), p = dir / "n";
    const int writers = 4, each = 100;
#ifdef _WIN32
    SetEnvironmentVariableW(L"CAS_PATH", p.c_str());
#else
    setenv("CAS_PATH", p.c_str(), 1);
#endif
    Child reader = spawn("reader", writers * each);
    std::vector<Child> ws;
    for (int w = 0; w < writers; ++w) ws.push_back(spawn("writer", each));
    bool clean = true;
    for (Child w : ws) clean = join(w) == 0 && clean;
    if (!clean) kill(reader);
    check(clean, "every writer process finished");
    check(join(reader) == 0, "the reader saw the final value");
    int got = counter(read(p));
    check(got == writers * each, "no lost update across processes",
          std::to_string(got) + " of " + std::to_string(writers * each) + " survived");
    check(entries(dir) == 2, "contention leaves nothing behind", std::to_string(entries(dir)));
    fs::remove_all(dir);
}

struct Ended {};

static void an_ended_record_stays_ended() {
    fs::path dir = fresh_dir(), p = dir / "r";
    std::mutex mu;
    int refused = 0, applied = 0, failed = 0;
    auto step = [](const Value& cur) -> std::string {
        if (cur == "done") throw Ended{};
        return "running";
    };
    std::vector<std::thread> ts;
    for (int i = 0; i < 16; ++i) {
        ts.emplace_back([&] {
            try {
                change(p, step);
                std::lock_guard<std::mutex> g(mu);
                ++applied;
            } catch (const Ended&) {
                std::lock_guard<std::mutex> g(mu);
                ++refused;
            } catch (const std::exception&) {
                std::lock_guard<std::mutex> g(mu);
                ++failed;
            }
        });
    }
    ts.emplace_back([&] { change(p, [](const Value&) { return std::string("done"); }); });
    for (auto& t : ts) t.join();
    check(read(p) == "done", "an ended record stays ended",
          "walked backwards after " + std::to_string(refused) + " refusals");
    check(failed == 0 && refused + applied == 16, "every edit either applied or refused",
          std::to_string(refused) + " refused, " + std::to_string(applied) + " applied, " + std::to_string(failed) + " failed");
    fs::remove_all(dir);
}

static std::string files(const fs::path& dir) {
    std::vector<std::string> found;
    for (const auto& e : fs::recursive_directory_iterator(dir)) {
        if (e.is_regular_file()) found.push_back(e.path().lexically_relative(dir).generic_string());
    }
    std::sort(found.begin(), found.end());
    std::string out;
    for (const auto& f : found) out += (out.empty() ? "" : ",") + f;
    return out;
}

static void placement_keeps_locks_and_staging_out_of_root() {
    fs::path dir = fresh_dir();
    Placement store(dir / "root", dir / "side");
    fs::path p = store.root() / "host" / "ns" / "model" / "latest";
    store.write(p, std::nullopt, "1");
    bool refused = false;
    try {
        store.write(p, std::nullopt, "2");
    } catch (const Moved&) {
        refused = true;
    }
    check(refused, "a placement refuses a stale base");
    store.change(p, [](const Value& cur) { return *cur + "!"; });
    check(store.read(p) == "1!", "a placement reads what it wrote");
    check(files(store.root()) == "host/ns/model/latest", "root holds only the data file", files(store.root()));
    check(files(store.side()) == "host/ns/model/latest.lock", "side holds the lock", files(store.side()));
    fs::remove_all(dir);
}

static bool outside(const Placement& store, const fs::path& p) {
    try {
        store.write(p, std::nullopt, "x");
        return false;
    } catch (const OutsideRoot&) {
        return true;
    }
}

static void placement_refuses_what_it_does_not_cover() {
    fs::path dir = fresh_dir();
    Placement store(dir / "root", dir / "side");
    check(outside(store, store.root()), "a placement refuses its root");
    check(outside(store, dir / "elsewhere"), "a placement refuses a path outside root");
    check(outside(store, store.side() / "x"), "a placement refuses a path in side");
    check(outside(store, store.root() / ".." / "escape"), "a placement refuses a path escaping root");
    bool contains = false;
    try {
        Placement bad(store.root(), dir);
    } catch (const std::invalid_argument&) {
        contains = true;
    }
    check(contains, "a side directory containing root is refused");
    fs::remove_all(dir);
}

static void placement_refuses_a_real_second_volume() {
    fs::path dir = fresh_dir();
    std::vector<fs::path> candidates;
#ifdef _WIN32
    for (char c = 'A'; c <= 'Z'; ++c) candidates.push_back(std::string(1, c) + ":\\");
#else
    candidates = {"/dev/shm", "/run"};
#endif
    for (const fs::path& other : candidates) {
        std::error_code ec;
        if (!fs::is_directory(other, ec)) continue;
        try {
            Placement same(dir / "root", other);
        } catch (const CrossVolume&) {
            check(true, "a side directory on another volume is refused");
            fs::remove_all(dir);
            return;
        } catch (const std::exception&) {
        }
    }
    std::printf("  SKIP  no second volume on this machine\n");
    fs::remove_all(dir);
}

static void placement_loses_no_update_across_processes() {
    fs::path dir = fresh_dir();
    Placement store(dir / "root", dir / "side");
    fs::path p = store.root() / "sub" / "n";
    const int writers = 4, each = 100;
    set_env("CAS_PATH", p);
    set_env("CAS_ROOT", store.root());
    set_env("CAS_SIDE", store.side());
    Child reader = spawn("reader", writers * each);
    std::vector<Child> ws;
    for (int w = 0; w < writers; ++w) ws.push_back(spawn("writer", each));
    bool clean = true;
    for (Child w : ws) clean = join(w) == 0 && clean;
    if (!clean) kill(reader);
    check(clean, "every placement writer process finished");
    check(join(reader) == 0, "the placement reader saw the final value");
    int got = counter(store.read(p));
    check(got == writers * each, "no lost update across processes through a placement",
          std::to_string(got) + " of " + std::to_string(writers * each) + " survived");
    check(files(store.root()) == "sub/n", "contention leaves nothing in root", files(store.root()));
    check(files(store.side()) == "sub/n.lock", "contention leaves only the lock in side", files(store.side()));
    set_env("CAS_ROOT", fs::path());
    set_env("CAS_SIDE", fs::path());
    fs::remove_all(dir);
}

static std::vector<std::pair<fs::path, Value>> synced;

static void directory_is_synced_after_the_rename() {
    fs::path dir = fresh_dir(), p = dir / "v";
    static fs::path watched;
    watched = p;
    synced.clear();
    testing::after_directory_sync = [](const fs::path& d) { synced.emplace_back(d, read(watched)); };
    write(p, std::nullopt, "first");
    testing::after_directory_sync = nullptr;
    check(synced.size() == 1, "one directory sync per write", std::to_string(synced.size()));
    check(!synced.empty() && synced[0].first == p.parent_path(), "the target's directory is synced");
    check(!synced.empty() && synced[0].second == "first", "the directory is synced after the rename");
    fs::remove_all(dir);
}

static void sweep_removes_only_staged_files() {
    fs::path dir = fresh_dir(), p = dir / "n";
    write(p, std::nullopt, "1 1");
    for (const char* name : {"n.4242.tmp", "n.a_b9.tmp", "m.123.tmp", "n.lock.123.tmp", "n.x.y.tmp", "n.tmp"}) {
        std::ofstream(dir / name) << "x";
    }
    check(sweep(p) == 2, "sweep removes the staged files of the path");
    check(files(dir) == "m.123.tmp,n,n.lock,n.lock.123.tmp,n.tmp,n.x.y.tmp", "sweep leaves other names alone", files(dir));
    check(sweep(p) == 0, "a second sweep finds nothing");
    fs::remove_all(dir);
}

static bool wait_for(const fs::path& marker) {
    for (int i = 0; i < 3000; ++i) {
        std::error_code ec;
        if (fs::exists(marker, ec)) return true;
        std::this_thread::sleep_for(std::chrono::milliseconds(10));
    }
    return false;
}

static void killed_writer(bool placed) {
    fs::path dir = fresh_dir();
    std::optional<Placement> store;
    fs::path p = dir / "n";
    if (placed) {
        store.emplace(dir / "root", dir / "side");
        p = store->root() / "m" / "latest";
    }
    auto do_write = [&](const Value& base, const std::string& data) {
        if (store) store->write(p, base, data); else write(p, base, data);
    };
    do_write(std::nullopt, "1 1");
    fs::path marker = dir / "staged.marker";
    set_env("CAS_PATH", p);
    set_env("CAS_MARKER", marker);
    set_env("CAS_ROOT", placed ? store->root() : fs::path());
    set_env("CAS_SIDE", placed ? store->side() : fs::path());
    Child child = spawn("killed", 1);
    bool staged = wait_for(marker);
    kill(child);
    join(child);
    set_env("CAS_ROOT", fs::path());
    set_env("CAS_SIDE", fs::path());
    set_env("CAS_MARKER", fs::path());
    std::string label = placed ? "placement: " : "beside: ";
    check(staged, label + "the killed writer staged before it was killed");
    check((store ? store->read(p) : read(p)) == "1 1", label + "a killed writer leaves the previous value");
    if (placed) {
        check(files(store->root()) == "m/latest", label + "root holds only the data file", files(store->root()));
    }
    std::this_thread::sleep_for(std::chrono::milliseconds(100));
    if (store) store->change(p, increment); else change(p, increment);
    check((store ? store->read(p) : read(p)) == "2 2", label + "the killed writer's lock was released");
    int gone = store ? store->sweep(p) : sweep(p);
    check(gone == 1, label + "sweep removes the killed writer's staged file", std::to_string(gone));
    if (placed) {
        check(files(store->side()) == "m/latest.lock", label + "side holds only the lock after the sweep", files(store->side()));
    } else {
        check(files(dir) == "n,n.lock,staged.marker", label + "only the file, its lock and the marker remain", files(dir));
    }
    fs::remove_all(dir);
}

static fs::path subject() { return env_path("CAS_PATH"); }

int main() {
    if (const char* name = std::getenv("CAS_ROLE")) {
        return role(name, subject(), std::atoi(std::getenv("CAS_N")));
    }
#ifdef _WIN32
    _setmode(_fileno(stdout), _O_BINARY);
#endif
    std::printf("cas\n");
    stale_write_is_refused();
    missing_reads_as_nullopt();
    missing_to_empty_is_a_change();
    no_lost_update_in_process();
    no_lost_update_across_processes();
    an_ended_record_stays_ended();
    placement_keeps_locks_and_staging_out_of_root();
    placement_refuses_what_it_does_not_cover();
    placement_refuses_a_real_second_volume();
    placement_loses_no_update_across_processes();
    directory_is_synced_after_the_rename();
    sweep_removes_only_staged_files();
    killed_writer(false);
    killed_writer(true);
    return failures == 0 ? 0 : 1;
}
