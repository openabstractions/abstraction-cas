"""Writers of all three languages on one file, a counter or a job record. A lock they do not share shows as a lost update; a rename they read differently shows as a torn read."""

import os
import subprocess
import sys
import tempfile
import time

here = os.path.dirname(os.path.abspath(__file__))
repo = os.path.dirname(here)
sys.path.insert(0, os.path.join(here, "python"))
sys.path.insert(0, os.path.join(repo, "abstraction-job", "python"))
import abstraction_cas as cas

per, each = 2, 100
exe = ".exe" if sys.platform == "win32" else ""


def tricky_name():
    lines = open(os.path.join(here, "names.list"), encoding="utf-8").read().splitlines()
    return [l for l in lines if l and not l.startswith(">")][-1]


def go_test_binary(build, module, **env):
    out = os.path.join(build, os.path.basename(os.path.dirname(module)) + ".test" + exe)
    subprocess.run(["go", "test", "-c", "-o", out, "."], cwd=module, env=dict(os.environ, **env), check=True)
    return out


def cpp(var, binary):
    p = os.environ.get(var, "")
    if not os.path.isfile(p):
        sys.exit("c++: ABSENT - %s must name the %s the C++ build produced; without it this proves two languages, not three" % (var, binary))
    return p


class Counter:
    name, env = "cas", "CAS"

    def commands(self, build):
        return {
            "cpp": [cpp("CAS_CPP", "test_cas")],
            "go": [go_test_binary(build, os.path.join(here, "go"), GOWORK="off"), "-test.run=^$"],
            "python": [sys.executable, os.path.join(here, "python", "test_abstraction_cas.py")],
        }

    def subject(self, d):
        return os.path.join(d, tricky_name())

    def final(self, path):
        return cas.read(path)

    def want(self, total):
        return b"%d %d" % (total, total)


class JobRecord:
    name, env = "job", "JOB"

    def commands(self, build):
        return {
            "cpp": [cpp("JOB_CPP", "test_job_store")],
            "go": [go_test_binary(build, os.path.join(repo, "abstraction-job", "go")), "-test.run=^$"],
            "python": [sys.executable, os.path.join(repo, "abstraction-job", "python", "test_abstraction_job.py")],
        }

    def subject(self, d):
        import abstraction_job as job
        store = job.FileStore(d)
        jid = store.submit(job.Record(id="", kind="mixed", spec={"writers": per * 3}))
        store.claim(jid, "mixed", 3600)
        return os.path.join(d, "jobs", jid + ".json")

    def final(self, path):
        import abstraction_job as job
        store = job.FileStore(os.path.dirname(os.path.dirname(path)))
        return store.load(os.path.basename(path)[: -len(".json")]).progress.done

    def want(self, total):
        return total


def spawn(prefix, cmd, role, path, n):
    env = dict(os.environ, **{prefix + "_PATH": path, prefix + "_ROLE": role, prefix + "_N": str(n)})
    return subprocess.Popen(cmd, env=env)


def one_run(subject, cmds, where, i):
    path = subject.subject(tempfile.mkdtemp(dir=where))
    total = per * each * len(cmds)
    started = time.perf_counter()
    readers = [(l, spawn(subject.env, c, "reader", path, total)) for l, c in cmds.items()]
    writers = [(l, spawn(subject.env, c, "writer", path, each)) for l, c in cmds.items() for _ in range(per)]
    codes = [(l, p.wait()) for l, p in writers]
    got, want = subject.final(path), subject.want(total)
    faults = [] if got == want else ["final value %r, wanted %r" % (got, want)]
    if faults:
        for _, r in readers:
            r.kill()
    codes += [(l, p.wait()) for l, p in readers]
    seconds = time.perf_counter() - started
    left = os.listdir(os.path.dirname(path))
    faults += [l + " exit %d" % c for l, c in codes if c]
    if len(left) != 2:
        faults.append("%d entries beside the file" % len(left))
    print("run %d: %s in %.1f s%s" % (i, "ok" if not faults else "FAILED", seconds, "".join("; " + f for f in faults)))
    return not faults


def main(argv):
    args = [a for a in argv if not a.startswith("--")]
    subject = JobRecord() if "--job" in argv else Counter()
    runs = int(args[0]) if args else 3
    where = args[1] if len(args) > 1 else None
    with tempfile.TemporaryDirectory() as build:
        cmds = subject.commands(build)
        green = sum(one_run(subject, cmds, where or build, i + 1) for i in range(runs))
    print("%s %s: %d of %d runs green; %d writers x %d in go, c++, python, one reader per language, in %s" % (
        sys.platform, subject.name, green, runs, per * len(cmds), each, where or "a temporary directory"))
    return 0 if green == runs else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
