"""text-generation-webui's download-model.py, which pages the tree with a
cursor it builds itself and checks LFS files' SHA-256 with --check.

The script is AGPL-3.0, so it's fetched at test time from a pinned commit
rather than copied into this repository."""
import os
import subprocess
import sys
import urllib.request

from conftest import TINY

TGW_COMMIT = "c93f8871239550de2ccfe1e95d469aa82616f07e"


def test_download_model_script(proxy, tmp_path):
    script = tmp_path / "download-model.py"
    url = f"https://raw.githubusercontent.com/oobabooga/text-generation-webui/{TGW_COMMIT}/download-model.py"
    script.write_bytes(urllib.request.urlopen(url, timeout=60).read())
    # The one module it imports from the webui.
    (tmp_path / "modules").mkdir()
    (tmp_path / "modules" / "__init__.py").write_text("")
    (tmp_path / "modules" / "paths.py").write_text(
        "from pathlib import Path\ndef resolve_user_data_dir(*a, **k):\n    return Path('.')\n")

    out = tmp_path / "out"
    env = dict(os.environ, HF_ENDPOINT=proxy.url, PYTHONPATH=str(tmp_path))

    def run(*args):
        res = subprocess.run([sys.executable, str(script), TINY, "--output", str(out), *args],
                             env=env, capture_output=True, text=True, timeout=600, cwd=tmp_path)
        assert res.returncode == 0, res.stdout[-2000:] + res.stderr[-2000:]
        return res.stdout + res.stderr

    run()
    assert (out / "config.json").exists() and (out / "pytorch_model.bin").exists()
    # --check only validates (and exits 0 either way), so read its verdict.
    verdict = run("--check")
    assert "[+] Validated checksums" in verdict, verdict[-2000:]
