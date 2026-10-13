#!/usr/bin/env python3
# SPDX-License-Identifier: CC-BY-4.0
"""Print an HS256 token for the subjects example. Stands in for your identity provider.

Usage: mint-jwt.py <claims.json>
Signs the claims with the demo secret from serve.sh and adds "exp" (5 minutes).
"""

import base64
import hashlib
import hmac
import json
import sys
import time

SECRET = b"subjects-demo-jwt-secret"


def b64url(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


claims = json.load(open(sys.argv[1]))
claims["exp"] = int(time.time()) + 300
header = b64url(json.dumps({"alg": "HS256", "typ": "JWT"}).encode())
payload = b64url(json.dumps(claims).encode())
signature = hmac.new(SECRET, f"{header}.{payload}".encode(), hashlib.sha256).digest()
print(f"{header}.{payload}.{b64url(signature)}")
