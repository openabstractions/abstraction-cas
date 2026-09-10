# abstraction-cas, in Python

Compare-and-set over a file, on the kernel's own lock. Two writers on one file do
not lose each other's updates, and a writer killed halfway leaves the old file
intact rather than half a new one. One module, standard library only, importing
nothing of ours.

This page is the Python package. The contract, the Go and C++ implementations,
what is measured and what is `UNPROVEN` are on
[the repository](https://github.com/openabstractions/abstraction-cas).

## Install

Not on PyPI, and the name on PyPI is not ours.

    git clone https://github.com/openabstractions/abstraction-cas
    pip install ./abstraction-cas/python

Python 3.9 or later. `abstraction_cas.py` is one file with no imports of ours, so
copying it into a `_vendor/` directory of your own is an equally complete
installation.

## An example that runs

Read, edit and write under one lock, so an edit always sees the truth and never
needs a retry:

```python
import json
import abstraction_cas as cas

def add_app(current):
    known = json.loads(current) if current else {"apps": []}
    known["apps"].append("comfyui")
    return json.dumps(known).encode()

cas.change("apps.json", add_app)
print(cas.read("apps.json").decode())
```

It prints `{"apps": ["comfyui"]}`. Run it twice and the list has two entries: the
edit saw what the first run wrote.

## What an application calls

| call | what it does |
|---|---|
| `read(path)` | the whole file, or `None` when there is none |
| `write(path, base, data)` | replace the file only if it still holds `base`. `None` as `base` means "only if it does not exist"; otherwise `Moved` |
| `change(path, edit)` | read, edit and write under the lock. `edit` takes the current bytes (or `None`) and returns the new ones |
| `sweep(path)` | remove the temporaries a killed writer left beside `path`; returns how many |

`Moved` is raised when the file changed since it was read, `Reentrant` when a
write runs inside an edit on the same file.

The invariants, each with the test that proves it in each language, are on
[the repository's README](https://github.com/openabstractions/abstraction-cas);
none is restated here.

## What may break

- **No conformance verdict.** No scenario in the suite cites this layer yet —
  [what is proven and what is not](https://openabstractions.org/coverage.html).
  The invariants are covered by this layer's own tests, which is a weaker claim.
- **Not on any package index**, and no release carries an API stability promise.
  Pin a commit you have read.
- **One lock and one rename are the whole mechanism**, so two machines writing
  one file over a network filesystem are only as safe as that filesystem's lock.
- Every published transcript was produced on Windows or Linux. macOS is
  `UNPROVEN` throughout.

Apache-2.0. See [LICENSE](LICENSE).
