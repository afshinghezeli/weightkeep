# Using weightkeep with your tools

Start the endpoint once:

```sh
weightkeep serve            # http://127.0.0.1:8700
```

Every example below is exercised by the compatibility suite in `test/compat`, against the real client
and huggingface.co, except where a section says otherwise.

## transformers, diffusers, vLLM, sentence-transformers, the hf CLI

Anything built on `huggingface_hub` reads `HF_ENDPOINT`:

```sh
export HF_ENDPOINT=http://127.0.0.1:8700
python -c "from transformers import AutoTokenizer; AutoTokenizer.from_pretrained('HuggingFaceTB/SmolLM2-135M')"
hf download HuggingFaceTB/SmolLM2-135M
```

Files download through weightkeep, get verified and kept, and land in the normal Hugging Face cache
as usual.

If a machine used huggingface.co directly before, its cache may hold tree listings that point
`hf_xet` at Hugging Face's storage servers, which bypasses `HF_ENDPOINT`. weightkeep strips those
signals from everything it serves, but it can't change what is already cached. Setting
`HF_HUB_DISABLE_XET=1` rules it out.

Without any endpoint running, `weightkeep export REPO` writes a kept model into the Hugging Face cache
so `HF_HUB_OFFLINE=1` works on its own.

## llama.cpp

llama.cpp's `-hf` flag reads `MODEL_ENDPOINT` first, then `HF_ENDPOINT`:

```sh
export MODEL_ENDPOINT=http://127.0.0.1:8700/
llama-completion -hf bartowski/SmolLM2-135M-Instruct-GGUF:Q4_K_M -p "The capital of France is"
```

This needs a llama.cpp build from March 2026 or later, which resolves the commit through
`/api/models/{repo}/refs`. Older builds use a manifest endpoint weightkeep also answers, but they
aren't in the test suite.

To use a file directly with `-m`, export it to a directory:

```sh
weightkeep export bartowski/SmolLM2-135M-Instruct-GGUF --to ./models/smollm
```

## Ollama

Ollama has no endpoint setting; the registry host is part of the model name. Use the address
`weightkeep serve` listens on, with `--insecure` because it's plain HTTP:

```sh
ollama pull 127.0.0.1:8700/bartowski/SmolLM2-135M-Instruct-GGUF:Q4_K_M --insecure
ollama run 127.0.0.1:8700/bartowski/SmolLM2-135M-Instruct-GGUF:Q4_K_M
```

The model is stored in Ollama under that name, not under `hf.co/...`. The GGUF itself is the same blob
weightkeep keeps for every other client, so pulling it for Ollama and llama.cpp costs the disk space
once in weightkeep's store (plus Ollama's own copy).

## text-generation-webui

Its `download-model.py` reads `HF_ENDPOINT`:

```sh
HF_ENDPOINT=http://127.0.0.1:8700 python download-model.py HuggingFaceTB/SmolLM2-135M
```

## LM Studio

LM Studio has no documented way to change the Hub endpoint, and weightkeep doesn't intercept
`huggingface.co` traffic. Export the GGUF to a directory and import it from there:

```sh
weightkeep export bartowski/SmolLM2-135M-Instruct-GGUF --to ~/.lmstudio/models/bartowski/SmolLM2-135M-Instruct-GGUF
```

This one isn't covered by the test suite.

## Running beyond localhost

`weightkeep serve --addr 0.0.0.0:8700` works for a home lab, but anyone who can reach the port can read
every kept model, including gated ones you pulled with your own token. Put it behind something that
authenticates if that matters.
