#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Run every example under examples/ and compare its output with golden files.

Layout of examples/<name>/ (see examples/README.md):
  policies.d/           policy objects
  tests/*.yaml          suites for `sluice policy test policies.d --tests tests`
  serve.sh              starts a local server on :8080 (admin on :9091)
  steps/NN-name.sh      commands, run in order from the example directory
  expected/NN-name.txt  golden output of each step
  manual/               files a reader uses by hand (docker, systemd). Syntax check only.

Usage: docs-examples.py [--update] [--sluice PATH] [name ...]
"""

import argparse
import configparser
import difflib
import fcntl
import json
import os
import re
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
EXAMPLES = ROOT / "examples"
# REST, admin, MCP, and the helper processes of approval-workflow and rebac.
PORTS = (8080, 9091, 8081, 8090, 8091)
LOCK = Path(tempfile.gettempdir()) / "sluice-docs-examples.lock"
# The audit writer flushes in the background. Wait so `audit verify` sees every record.
SETTLE = 0.4

# JSON keys whose values differ between runs or between builds.
VOLATILE = {
    "query_id": "<query-id>",
    "approval_id": "<approval-id>",
    "accept_url": "<accept-url>",
    "reject_url": "<reject-url>",
    "hash": "<hash>",
    "prior_hash": "<hash>",
    "timestamp": "<time>",
    "created_at": "<time>",
    "requested_at": "<time>",
    "decided_at": "<time>",
    "expires_at": "<time>",
    "last_check": "<time>",
    "schema_pulled_at": "<time>",
    "build_time": "<time>",
    "remote_ip": "<remote-ip>",
    "uptime": "<duration>",
    "version": "<version>",
    "sluice_version": "<version>",
    "duckdb_version": "<version>",
    "commit": "<commit>",
    "digest": "<digest>",
    "config_digest": "<digest>",
    "go": "<go-version>",
}
# Durations: the server omits a zero value, so the key is removed from every output.
DROPPED = {"duration_ms", "latency_ms"}
ID_RULE = (re.compile(r"\b[0-7][0-9A-HJKMNP-TV-Z]{25}\b"), "<id>")
# Applied to plain text lines only. JSON is handled by key, so sample data stays as it is.
TEXT_RULES = [
    (re.compile(r"\b\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})"), "<time>"),
    (re.compile(r"\b\d{4}-\d{2}-\d{2}\b"), "<date>"),
    (re.compile(r"\b[0-9a-f]{64}\b"), "<hash>"),
    (re.compile(r"(?<=hash=)[0-9a-f]{8,}…?"), "<hash>"),
    (re.compile(r"(?<=digest )[0-9a-f]{12}\b"), "<digest>"),
    (re.compile(r"\b\d+(\.\d+)?(ns|µs|ms)\b"), "<duration>"),
    ID_RULE,
]


def scrub(value, key=""):
    if isinstance(value, dict):
        if key == "extras" and "origin" in value:
            value = dict(value, origin="<host>")
        return {k: VOLATILE[k] if k in VOLATILE and v else scrub(v, k)
                for k, v in value.items() if k not in DROPPED}
    if isinstance(value, list):
        return [scrub(v) for v in value]
    if isinstance(value, str) and value[:1] in "{[":
        # JSON inside a string, as in MCP tool results.
        try:
            return json.dumps(scrub(json.loads(value)), ensure_ascii=False)
        except ValueError:
            pass
    return ID_RULE[0].sub(ID_RULE[1], value) if isinstance(value, str) else value


def pretty(value, depth=0):
    """Indented JSON. Lists of plain values stay on one line, so a row is one line."""
    pad = "  " * depth
    if isinstance(value, dict) and value:
        body = [f"{pad}  {json.dumps(k)}: {pretty(v, depth + 1)}" for k, v in value.items()]
        return "{\n" + ",\n".join(body) + f"\n{pad}}}"
    if isinstance(value, list) and any(isinstance(v, (dict, list)) for v in value):
        body = [f"{pad}  {pretty(v, depth + 1)}" for v in value]
        return "[\n" + ",\n".join(body) + f"\n{pad}]"
    return json.dumps(value, ensure_ascii=False)


def normalize(raw: str, keep: set[str]) -> str:
    """Format JSON for reading and replace values that change between runs."""
    text = raw.strip("\n")
    try:
        return pretty(scrub(json.loads(text))) + "\n"
    except ValueError:
        pass
    lines = []
    for line in text.splitlines():
        # MCP over HTTP answers as server-sent events: "data: {json}".
        prefix = "data: " if line.startswith("data: ") else ""
        try:
            doc = json.loads(line[len(prefix):])
        except ValueError:
            doc = None
        if isinstance(doc, (dict, list)):
            body = pretty(scrub(doc)) if prefix else json.dumps(scrub(doc), ensure_ascii=False)
            lines.append(prefix + body)
            continue
        for pattern, placeholder in TEXT_RULES:
            if placeholder not in keep:
                line = pattern.sub(placeholder, line)
        lines.append(line.rstrip())
    return "\n".join(lines) + "\n"


def port_open(port: int) -> bool:
    with socket.socket() as s:
        s.settimeout(0.5)
        return s.connect_ex(("127.0.0.1", port)) == 0


def healthy() -> bool:
    try:
        with socket.create_connection(("127.0.0.1", PORTS[0]), timeout=1) as s:
            s.sendall(b"GET /v1/health HTTP/1.0\r\nHost: localhost\r\n\r\n")
            return b" 200 " in s.recv(64)
    except OSError:
        return False


class Server:
    """Runs serve.sh in its own process group so every child stops with it."""

    def __init__(self, example: Path, env: dict):
        busy = [p for p in PORTS if port_open(p)]
        if busy:
            raise RuntimeError(f"port {busy[0]} is in use, stop the process that holds it")
        self.log = tempfile.NamedTemporaryFile("w+", prefix="sluice-example-", suffix=".log")
        self.proc = subprocess.Popen(["bash", "serve.sh"], cwd=example, env=env, stdout=self.log,
                                     stderr=subprocess.STDOUT, start_new_session=True)
        deadline = time.time() + 60
        while not healthy():
            if self.proc.poll() is not None or time.time() > deadline:
                self.stop()
                raise RuntimeError("serve.sh did not become healthy:\n" + self.tail())
            time.sleep(0.2)

    def tail(self) -> str:
        self.log.seek(0)
        return "".join(self.log.readlines()[-20:])

    def stop(self):
        for sig in (signal.SIGTERM, signal.SIGKILL):
            try:
                os.killpg(self.proc.pid, sig)
            except ProcessLookupError:
                break
            try:
                self.proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                continue
            while any(port_open(p) for p in PORTS):
                time.sleep(0.1)
            break


def check_manual(example: Path) -> list[str]:
    """Files the harness cannot run get a syntax check."""
    errors = []
    files = [f for f in (example / "manual").rglob("*") if f.is_file()]
    files += list(example.glob("docker-compose.yaml"))
    for f in files:
        try:
            if f.suffix == ".sh":
                subprocess.run(["bash", "-n", str(f)], check=True, capture_output=True, text=True)
            elif f.suffix == ".json":
                json.loads(f.read_text())
            elif f.suffix in (".yaml", ".yml"):
                import yaml  # from docs/requirements.txt
                list(yaml.safe_load_all(f.read_text()))
            elif f.suffix == ".service":
                configparser.ConfigParser(strict=False, interpolation=None).read_string(f.read_text())
        except subprocess.CalledProcessError as e:
            errors.append(f"{f.relative_to(ROOT)}: {e.stderr.strip()}")
        except Exception as e:  # noqa: BLE001
            errors.append(f"{f.relative_to(ROOT)}: {e}")
    return errors


def run_example(example: Path, env: dict, update: bool) -> list[str]:
    errors = check_manual(example)
    steps = sorted((example / "steps").glob("*.sh"))
    suites = sorted((example / "tests").glob("*.yaml"))
    if not steps and not suites:
        return ["has no tests/ and no steps/, so nothing checks it"]
    shutil.rmtree(example / "data", ignore_errors=True)

    if suites:
        r = subprocess.run(["sluice", "policy", "test", "policies.d", "--tests", "tests"],
                           cwd=example, env=env, capture_output=True, text=True)
        if r.returncode != 0:
            errors.append("policy test failed:\n" + r.stdout + r.stderr)

    keep_file = example / "expected" / "keep"
    keep = set(keep_file.read_text().split()) if keep_file.exists() else set()
    server = None
    try:
        if (example / "serve.sh").exists():
            server = Server(example, env)
        for step in steps:
            if server:
                time.sleep(SETTLE)
            r = subprocess.run(["bash", str(step.relative_to(example))], cwd=example, env=env,
                               stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
                               timeout=120)
            got = normalize(r.stdout, keep)
            if r.returncode != 0:
                got += f"[exit status {r.returncode}]\n"
            golden = example / "expected" / (step.stem + ".txt")
            if update:
                golden.parent.mkdir(exist_ok=True)
                golden.write_text(got)
            elif not golden.exists():
                errors.append(f"{golden.relative_to(ROOT)} is missing, run `make docs-update`")
            elif golden.read_text() != got:
                diff = difflib.unified_diff(golden.read_text().splitlines(), got.splitlines(),
                                            "expected", "got", lineterm="")
                errors.append(f"{step.relative_to(ROOT)}: output differs\n" + "\n".join(diff))
    except (RuntimeError, subprocess.TimeoutExpired) as e:
        errors.append(str(e))
    finally:
        if server:
            server.stop()

    known = {s.stem + ".txt" for s in steps} | {"keep"}
    for f in sorted((example / "expected").glob("*")):
        if f.name not in known:
            errors.append(f"{f.relative_to(ROOT)} has no step")
    return errors


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("names", nargs="*", help="examples to run (default: all)")
    ap.add_argument("--update", action="store_true", help="rewrite the golden files")
    ap.add_argument("--sluice", default=str(ROOT / "bin" / "sluice"), help="sluice binary")
    args = ap.parse_args()

    sluice = Path(args.sluice).resolve()
    if not sluice.is_file():
        print(f"{sluice} not found, run `make build`", file=sys.stderr)
        return 1
    env = dict(os.environ, PATH=f"{sluice.parent}{os.pathsep}{os.environ['PATH']}", LC_ALL="C")

    names = args.names or sorted(p.name for p in EXAMPLES.iterdir() if p.is_dir())
    failed = 0
    # One run at a time on this machine: every server example uses the same ports.
    with open(LOCK, "w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        for name in names:
            example = EXAMPLES / name
            if not example.is_dir():
                print(f"FAIL  {name}: no such example")
                failed += 1
                continue
            errors = run_example(example, env, args.update)
            print(f"{'FAIL' if errors else 'ok  '}  {name}")
            for e in errors:
                print("      " + e.replace("\n", "\n      "))
            failed += bool(errors)
    print(f"\n{len(names) - failed} passed, {failed} failed")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
