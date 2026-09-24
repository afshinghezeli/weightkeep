#!/usr/bin/env bash
# Runs real Hugging Face clients against `weightkeep serve`.
# Needs network access to huggingface.co and uv. llama.cpp and Ollama are
# optional: their tests are skipped when llama-completion or ollama isn't on
# PATH.
set -euo pipefail
cd "$(dirname "$0")/../.."
[ -x bin/weightkeep ] || make build
exec uv run --quiet --no-project \
  --with 'pytest>=8' \
  --with 'huggingface_hub>=1.0' \
  --with hf_xet \
  --with transformers \
  --with safetensors \
  --with numpy \
  --with requests \
  --with tqdm \
  pytest test/compat -q "$@"
