"""Pytest bootstrap: ensure the `risk/` dir is on sys.path so `import app...` works when running
pytest from anywhere. (We deliberately avoid packaging/install for this simulation service.)"""

import os
import sys

sys.path.insert(0, os.path.dirname(__file__))
