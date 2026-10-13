#!/usr/bin/env python3
# SPDX-License-Identifier: CC-BY-4.0
"""Create a SQLite database from a SQL file.

Usage: seed.py <seed.sql> <database.db>
"""

import sqlite3
import sys
from pathlib import Path

sql, db = Path(sys.argv[1]), Path(sys.argv[2])
db.parent.mkdir(parents=True, exist_ok=True)
db.unlink(missing_ok=True)
con = sqlite3.connect(db)
con.executescript(sql.read_text())
con.commit()
con.close()
