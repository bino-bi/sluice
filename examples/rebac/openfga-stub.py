#!/usr/bin/env python3
# SPDX-License-Identifier: CC-BY-4.0
"""Stub of the OpenFGA check endpoint for the rebac example. Listens on port 8091.

Usage: openfga-stub.py <tuples.json>

Answers POST /stores/<store>/check from the tuples in that file.
Appends each request body to data/checks.jsonl.
"""

import json
import signal
import socketserver
import sys
from pathlib import Path

TUPLES = json.loads(Path(sys.argv[1]).read_text())


class Check(socketserver.StreamRequestHandler):
    def handle(self):
        length = 0
        while line := self.rfile.readline().strip():
            name, _, value = line.partition(b":")
            if name.lower() == b"content-length":
                length = int(value)
        body = self.rfile.read(length)
        with open("data/checks.jsonl", "ab") as f:
            f.write(body + b"\n")
        answer = json.dumps({"allowed": json.loads(body)["tuple_key"] in TUPLES}).encode()
        self.wfile.write(b"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n")
        self.wfile.write(b"Content-Length: %d\r\nConnection: close\r\n\r\n" % len(answer))
        self.wfile.write(answer)


# A background job of a script ignores Ctrl-C. Undo that, so Ctrl-C stops the stub with Sluice.
signal.signal(signal.SIGINT, signal.SIG_DFL)
socketserver.TCPServer.allow_reuse_address = True
socketserver.TCPServer(("127.0.0.1", 8091), Check).serve_forever()
