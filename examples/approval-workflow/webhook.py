#!/usr/bin/env python3
# SPDX-License-Identifier: CC-BY-4.0
"""Webhook receiver for the approval-workflow example. Listens on port 8090.

Appends each webhook body to data/webhooks.jsonl.
Writes the newest values to data/accept_url, data/reject_url and data/approval_id.
"""

import json
import signal
import socketserver
from pathlib import Path

DATA = Path("data")


class Webhook(socketserver.StreamRequestHandler):
    def handle(self):
        length = 0
        while line := self.rfile.readline().strip():
            name, _, value = line.partition(b":")
            if name.lower() == b"content-length":
                length = int(value)
        body = self.rfile.read(length)
        with open(DATA / "webhooks.jsonl", "ab") as f:
            f.write(body + b"\n")
        payload = json.loads(body)
        for key in ("accept_url", "reject_url", "approval_id"):
            (DATA / key).write_text(payload[key] + "\n")
        self.wfile.write(b"HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n")


# A background job of a script ignores Ctrl-C. Undo that, so Ctrl-C stops the receiver with Sluice.
signal.signal(signal.SIGINT, signal.SIG_DFL)
socketserver.TCPServer.allow_reuse_address = True
socketserver.TCPServer(("127.0.0.1", 8090), Webhook).serve_forever()
