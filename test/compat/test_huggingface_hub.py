"""huggingface_hub (and so transformers, diffusers, vLLM) through the proxy."""
from conftest import SMOL, TINY, run_client


def test_snapshot_download_without_xet_traffic(proxy, tmp_path):
    out = run_client(f"""
        import os, hf_xet
        from huggingface_hub import snapshot_download
        p = snapshot_download("{SMOL}")
        print(sorted(os.listdir(p)))
    """, proxy.url, tmp_path)
    assert "model.safetensors" in out and "tokenizer.json" in out
    assert "xet" not in proxy.log(), "the client talked Xet through the proxy"


def test_hf_hub_download_and_errors(proxy, tmp_path):
    out = run_client(f"""
        import os
        from huggingface_hub import hf_hub_download
        from huggingface_hub.errors import EntryNotFoundError, RevisionNotFoundError, RepositoryNotFoundError
        p = hf_hub_download("{TINY}", "config.json")
        print("config", os.path.getsize(p))
        for call, exc in [
            (lambda: hf_hub_download("{TINY}", "no-such-file.bin"), EntryNotFoundError),
            (lambda: hf_hub_download("{TINY}", "config.json", revision="no-such-branch"), RevisionNotFoundError),
            (lambda: hf_hub_download("weightkeep-test/does-not-exist", "config.json"), RepositoryNotFoundError),
        ]:
            try:
                call()
                print("no error for", exc.__name__)
            except exc:
                print("raised", exc.__name__)
        # A 404 with X-Repo-Commit makes huggingface_hub cache the miss.
        print("no_exist", any("no-such-file.bin" in f for _, _, fs in os.walk(os.environ["HF_HUB_CACHE"]) for f in fs))
    """, proxy.url, tmp_path)
    assert "config " in out
    assert out.count("raised") == 3, out
    assert "no_exist True" in out


def test_pinned_commit_and_blobs(proxy, tmp_path):
    out = run_client(f"""
        from huggingface_hub import HfApi, hf_hub_download
        api = HfApi()
        info = api.model_info("{TINY}", files_metadata=True)
        lfs = [s for s in info.siblings if s.lfs]
        print("sha", info.sha, "lfs", len(lfs), "size", lfs[0].size)
        hf_hub_download("{TINY}", "config.json", revision=info.sha)
        files = api.list_repo_files("{TINY}", revision=info.sha)
        print("files", len(files))
    """, proxy.url, tmp_path)
    assert "lfs 1" in out and "files 5" in out, out


def test_transformers_through_offline_proxy(proxy, offline_proxy, tmp_path):
    # Warm the store through the online proxy, then load with only the
    # offline one running: nothing may reach huggingface.co.
    run_client(f'from huggingface_hub import snapshot_download; snapshot_download("{SMOL}")', proxy.url, tmp_path / "warm")
    out = run_client(f"""
        from transformers import AutoConfig, AutoTokenizer
        cfg = AutoConfig.from_pretrained("{SMOL}")
        tok = AutoTokenizer.from_pretrained("{SMOL}")
        print(cfg.model_type, tok("kept")["input_ids"])
    """, offline_proxy.url, tmp_path / "cold")
    assert out.startswith("llama ["), out
