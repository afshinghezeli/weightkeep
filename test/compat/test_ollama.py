"""Ollama pulling a GGUF through the proxy's /v2 registry routes."""
import json
import os
import shutil
import socket
import subprocess
import time
import urllib.request

import pytest

from conftest import GGUF

OLLAMA = shutil.which("ollama")


def free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


@pytest.fixture
def ollama(tmp_path):
    host = f"127.0.0.1:{free_port()}"
    env = dict(os.environ, OLLAMA_HOST=host, OLLAMA_MODELS=str(tmp_path / "models"))
    proc = subprocess.Popen([OLLAMA, "serve"], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    deadline = time.time() + 30
    while time.time() < deadline:
        try:
            urllib.request.urlopen(f"http://{host}/api/version", timeout=1)
            break
        except OSError:
            time.sleep(0.2)
    yield host, env
    proc.terminate()
    proc.wait(timeout=15)


@pytest.mark.skipif(OLLAMA is None, reason="ollama not installed")
def test_ollama_pull_and_generate(proxy, ollama):
    host, env = ollama
    name = proxy.url.removeprefix("http://") + f"/{GGUF}:Q4_K_M"
    res = subprocess.run([OLLAMA, "pull", name, "--insecure"], env=env, capture_output=True, text=True, timeout=600)
    assert res.returncode == 0, res.stdout[-1500:] + res.stderr[-1500:]

    req = urllib.request.Request(f"http://{host}/api/generate", data=json.dumps({
        "model": name, "prompt": "What is the capital of France? Answer in one word.",
        "stream": False, "options": {"temperature": 0},
    }).encode(), headers={"Content-Type": "application/json"})
    answer = json.load(urllib.request.urlopen(req, timeout=300))["response"]
    assert "Paris" in answer, answer
