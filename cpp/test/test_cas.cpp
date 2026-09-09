#define _CRT_SECURE_NO_WARNINGS
#include <abstraction/cas.h>

#include <cstdio>
#include <cstdlib>
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
extern char** environ;
#endif

namespace fs = std::filesystem;
using namespace abstraction::cas;

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

static int role(const std::string& name, const fs::path& path, int target) {
    try {
        if (name == "writer") {
            for (int i = 0; i < target; ++i) change(path, increment);
        } else if (name == "reader") {
            while (counter(read(path)) != target) {
            }
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

static Child spawn(const std::string& role, int n) {
    setenv("CAS_ROLE", role.c_str(), 1);
    setenv("CAS_N", std::to_string(n).c_str(), 1);
    std::string exe = fs::read_symlink("/proc/self/exe").string();
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

static fs::path subject() {
#ifdef _WIN32
    const wchar_t* p = _wgetenv(L"CAS_PATH");
#else
    const char* p = std::getenv("CAS_PATH");
#endif
    return p ? fs::path(p) : fs::path();
}

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
    return failures == 0 ? 0 : 1;
}
