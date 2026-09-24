"""Fixtures that run `weightkeep serve` for real clients to talk to.

Every client runs in its own subprocess: huggingface_hub reads HF_ENDPOINT
when it's imported, so one interpreter can't switch between servers.
Only small, permissively licensed repos are used.
"""
import os
import re
import subprocess
import sys
import textwrap
import time
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[2]
BIN = ROOT / "bin" / ("weightkeep.exe" if os.name == "nt" else "weightkeep")

# Tier A repos (see .claude/skills/licence-policy).
TINY = "prajjwal1/bert-tiny"  # MIT, 18 MB
SMOL = "HuggingFaceTB/SmolLM2-135M"  # Apache-2.0, 270 MB
GGUF = "bartowski/SmolLM2-135M-Instruct-GGUF"  # Apache-2.0


class Proxy:
    def __init__(self, store: Path, log: Path, *args: str):
        self.log_path = log
        env = dict(os.environ, WEIGHTKEEP_HOME=str(store))
        self._log = open(log, "w")
        self.proc = subprocess.Popen(
            [str(BIN), "serve", "--addr", "127.0.0.1:0", *args],
            env=env, stdout=subprocess.DEVNULL, stderr=self._log,
        )
        self.url = self._wait_for_url()

    def _wait_for_url(self) -> str:
        deadline = time.time() + 20
        while time.time() < deadline:
            m = re.search(r"on (http://127\.0\.0\.1:\d+)", self.log_path.read_text())
            if m:
                return m.group(1)
            if self.proc.poll() is not None:
                raise RuntimeError("weightkeep serve exited:\n" + self.log_path.read_text())
            time.sleep(0.1)
        raise RuntimeError("weightkeep serve did not start:\n" + self.log_path.read_text())

    def log(self) -> str:
        return self.log_path.read_text()

    def stop(self):
        self.proc.terminate()
        self.proc.wait(timeout=15)
        self._log.close()


@pytest.fixture(scope="session")
def store(tmp_path_factory) -> Path:
    return tmp_path_factory.mktemp("store")


@pytest.fixture(scope="session")
def proxy(store, tmp_path_factory):
    p = Proxy(store, tmp_path_factory.mktemp("logs") / "online.log")
    yield p
    p.stop()


@pytest.fixture
def offline_proxy(store, tmp_path):
    p = Proxy(store, tmp_path / "offline.log", "--offline")
    yield p
    p.stop()


def run_client(code: str, endpoint: str, cache: Path, **extra_env) -> str:
    """Runs Python code against endpoint and returns its stdout."""
    env = dict(os.environ, HF_ENDPOINT=endpoint, HF_HUB_CACHE=str(cache), HF_HUB_DISABLE_TELEMETRY="1")
    env.pop("HF_HUB_OFFLINE", None)
    env.pop("HF_HUB_DISABLE_XET", None)  # hf_xet stays enabled on purpose
    env.update({k: str(v) for k, v in extra_env.items()})
    res = subprocess.run([sys.executable, "-c", textwrap.dedent(code)], env=env,
                         capture_output=True, text=True, timeout=900)
    if res.returncode != 0:
        raise AssertionError(f"client failed:\n{res.stdout}\n{res.stderr}")
    return res.stdout
