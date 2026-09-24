"""`weightkeep export` writes a cache HF_HUB_OFFLINE=1 clients can load."""
import os
import subprocess

from conftest import BIN, SMOL, run_client


def test_export_then_offline_load(proxy, store, tmp_path):
    run_client(f'from huggingface_hub import snapshot_download; snapshot_download("{SMOL}")', proxy.url, tmp_path / "warm")
    cache = tmp_path / "exported"
    subprocess.run([str(BIN), "export", SMOL, "--cache-dir", str(cache)], check=True,
                   env=dict(os.environ, WEIGHTKEEP_HOME=str(store)), capture_output=True)
    out = run_client(f"""
        import os
        from huggingface_hub import snapshot_download
        from transformers import AutoTokenizer
        from safetensors import safe_open
        p = snapshot_download("{SMOL}")
        with safe_open(os.path.join(p, "model.safetensors"), framework="numpy") as f:
            print("tensors", len(list(f.keys())))
        print(AutoTokenizer.from_pretrained("{SMOL}")("kept")["input_ids"])
    """, "http://127.0.0.1:9", cache, HF_HUB_OFFLINE="1")
    assert "tensors 272" in out, out
