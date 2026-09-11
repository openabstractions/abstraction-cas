"""The five invariants of abstraction-cas/README.md, as stress. Mirrors abstraction-cas/go/cas_test.go."""

import os
import pathlib
import subprocess
import sys
import tempfile
import threading
import unittest

import abstraction_cas
from abstraction_cas import Moved, NoValue, Reentrant, change, read, sweep, write

names = pathlib.Path(__file__).resolve().parent.parent / "names.list"


def counter(cur):
    if cur is None:
        return 0
    a, b = cur.split()
    if a != b:
        raise AssertionError("torn read: %r" % cur)
    return int(a)


def increment(cur):
    n = counter(cur) + 1
    return b"%d %d" % (n, n)


def role(name, path, target):
    if name == "writer":
        for _ in range(target):
            change(path, increment)
    elif name == "reader":
        while counter(read(path)) != target:
            pass


class Ended(Exception):
    pass


def tricky_names():
    lines = names.read_text(encoding="utf-8").splitlines()
    return [l for l in lines if l and not l.startswith(">")]


class CasTest(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)

    def path(self, name):
        return os.path.join(self.dir.name, name)

    def test_stale_write_is_refused(self):
        p = self.path("v")
        with self.assertRaises(Moved):
            write(p, b"x", b"1")
        write(p, None, b"1")
        with self.assertRaises(Moved):
            write(p, None, b"2")
        with self.assertRaises(Moved):
            write(p, b"0", b"2")
        self.assertEqual(read(p), b"1")
        write(p, b"1", b"2")
        self.assertEqual(read(p), b"2")
        self.assertEqual(len(os.listdir(self.dir.name)), 2, "refused writes left files behind")

    def test_missing_reads_as_none(self):
        p = self.path("v")
        self.assertIsNone(read(p))
        write(p, None, b"")
        self.assertEqual(read(p), b"")

    def test_missing_to_empty_is_a_change(self):
        p = self.path("v")
        change(p, lambda cur: None)
        self.assertIsNone(read(p), "an edit that left the file missing created it")
        change(p, lambda cur: b"")
        self.assertEqual(read(p), b"", "an edit from missing to empty wrote nothing")

    def test_a_name_is_kept_whatever_is_in_it(self):
        for name in tricky_names():
            for pad in range(8):
                with self.subTest(name=ascii(name), pad=pad):
                    self.name_survives(name, pad)

    def name_survives(self, name, pad):
        d = os.path.join(tempfile.mkdtemp(dir=self.dir.name), "d" * (pad + 1))
        os.makedirs(d)
        p = os.path.join(d, name)
        write(p, None, b"first")
        self.assertEqual(read(p), b"first", "the bytes did not land under the name given")
        change(p, lambda cur: cur + b"!")
        self.assertEqual(read(p), b"first!")
        self.assertEqual(sorted(os.listdir(d)), sorted([name, name + ".lock"]))

    def test_a_change_inside_an_edit_is_refused(self):
        p = self.path("v")
        write(p, None, b"first")
        with self.assertRaises(Reentrant):
            change(p, lambda cur: change(p, lambda inner: b"inner"))
        with self.assertRaises(Reentrant):
            change(p, lambda cur: write(p, cur, b"inner"))
        self.assertEqual(read(p), b"first")
        other = self.path("w")
        change(p, lambda cur: (write(other, None, b"other"), b"second")[1])
        self.assertEqual(read(p), b"second", "a change on another file inside an edit was refused")
        self.assertEqual(read(other), b"other")

    def test_a_path_object_is_a_path(self):
        p = pathlib.Path(self.path("v"))
        self.assertIsNone(read(p))
        write(p, None, b"first")
        self.assertEqual(read(p), b"first")
        change(p, lambda cur: cur + b"!")
        self.assertEqual(read(p), b"first!")
        with self.assertRaises(Moved):
            write(p, None, b"x")

    def test_the_directory_is_flushed_after_the_rename(self):
        p = self.path("v")
        flushed = []
        real = abstraction_cas._fsync_dir

        def spy(directory):
            flushed.append((directory, read(p)))
            real(directory)

        abstraction_cas._fsync_dir = spy
        self.addCleanup(setattr, abstraction_cas, "_fsync_dir", real)
        write(p, None, b"first")
        self.assertEqual(len(flushed), 1, "the directory was not flushed after the rename")
        self.assertEqual(flushed[0][0], os.path.abspath(self.dir.name))
        self.assertEqual(flushed[0][1], b"first", "the directory was flushed before the rename")

    def test_a_long_path_works_where_open_does(self):
        p = self.path("L" * max(250 - len(self.dir.name) - 1, 8))
        self.assertGreaterEqual(len(p), 247, "the path is too short to reach the limit")
        with open(p, "wb") as f:
            f.write(b"bare")
        os.unlink(p)
        write(p, None, b"cas")
        self.assertEqual(read(p), b"cas")
        change(p, lambda cur: cur + b"!")
        self.assertEqual(read(p), b"cas!")

    def test_a_kill_between_stage_and_rename_leaves_nothing(self):
        p = self.path("v")
        write(p, None, b"first")
        killed = ("import os, sys\n"
                  "sys.path.insert(0, sys.argv[1])\n"
                  "import abstraction_cas as cas\n"
                  "cas._rename_over = lambda *a: os._exit(9)\n"
                  "cas.write(sys.argv[2], b'first', b'second')\n")
        child = subprocess.run([sys.executable, "-c", killed,
                                os.path.dirname(abstraction_cas.__file__), p])
        self.assertEqual(child.returncode, 9)
        self.assertEqual(len([n for n in os.listdir(self.dir.name) if n.endswith(".tmp")]), 1,
                         "the kill left nothing staged, so this proves nothing")
        write(p, b"first", b"second")
        self.assertEqual(read(p), b"second")
        self.assertEqual(sweep(p), 1)
        self.assertEqual(sorted(os.listdir(self.dir.name)), ["v", "v.lock"],
                         "a killed writer's staged file survived the sweep")
        self.assertEqual(sweep(p), 0)

    def test_an_edit_that_returns_no_value_is_refused(self):
        p = self.path("v")
        write(p, None, b"first")
        with self.assertRaises(NoValue):
            change(p, lambda cur: None)
        self.assertEqual(read(p), b"first")
        with self.assertRaises(NoValue):
            write(p, b"first", None)
        self.assertEqual(read(p), b"first")
        self.assertEqual(len(os.listdir(self.dir.name)), 2, "a refused write left files behind")

    def test_no_lost_update_in_process(self):
        p = self.path("n")
        writers, each = 8, 50
        errors = []

        def run():
            try:
                for _ in range(each):
                    change(p, increment)
            except Exception as e:
                errors.append(e)

        threads = [threading.Thread(target=run) for _ in range(writers)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(errors, [])
        self.assertEqual(counter(read(p)), writers * each)

    def test_no_lost_update_across_processes(self):
        p = self.path("n")
        writers, each = 4, 100

        def spawn(name, n):
            env = dict(os.environ, CAS_PATH=p, CAS_ROLE=name, CAS_N=str(n))
            return subprocess.Popen([sys.executable, __file__], env=env)

        reader = spawn("reader", writers * each)
        ws = [spawn("writer", each) for _ in range(writers)]
        codes = [w.wait() for w in ws]
        if any(codes):
            reader.kill()
        self.assertEqual(codes, [0] * writers)
        self.assertEqual(reader.wait(), 0)
        self.assertEqual(counter(read(p)), writers * each)
        self.assertEqual(len(os.listdir(self.dir.name)), 2, "contention left files behind")

    def test_an_ended_record_stays_ended(self):
        p = self.path("r")
        outcomes = []
        lock = threading.Lock()

        def step(cur):
            if cur == b"done":
                raise Ended()
            return b"running"

        def run(edit):
            try:
                change(p, edit)
                result = "applied"
            except Ended:
                result = "refused"
            except Exception as e:
                result = e
            with lock:
                outcomes.append(result)

        threads = [threading.Thread(target=run, args=(step,)) for _ in range(16)]
        threads.append(threading.Thread(target=run, args=(lambda cur: b"done",)))
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        self.assertEqual(read(p), b"done")
        self.assertEqual(sorted(o for o in outcomes if not isinstance(o, str)), [])
        self.assertEqual(len(outcomes), 17)


if __name__ == "__main__":
    if "CAS_ROLE" in os.environ:
        role(os.environ["CAS_ROLE"], os.environ["CAS_PATH"], int(os.environ["CAS_N"]))
    else:
        unittest.main()
