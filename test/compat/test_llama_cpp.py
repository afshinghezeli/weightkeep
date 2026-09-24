"""llama.cpp's -hf flag (refs, then an unpaginated tree, then resolve)."""
import os
import shutil
import subprocess

import pytest

from conftest import GGUF

LLAMA = shutil.which("llama-completion")


@pytest.mark.skipif(LLAMA is None, reason="llama-completion not installed")
def test_llama_cpp_hf_flag(proxy, tmp_path):
    env = dict(os.environ, MODEL_ENDPOINT=proxy.url + "/", LLAMA_CACHE=str(tmp_path), HF_HUB_CACHE=str(tmp_path))
    res = subprocess.run(
        [LLAMA, "-hf", f"{GGUF}:Q4_K_M", "-p", "The capital of France is", "-n", "8", "--temp", "0"],
        env=env, capture_output=True, text=True, stdin=subprocess.DEVNULL, timeout=600,
    )
    assert res.returncode == 0, res.stderr[-2000:]
    assert "Paris" in res.stdout
    assert "/refs status=200" in proxy.log()
