#!/usr/bin/env bash
# Cross-compile fury_agent for the supported OS/arch matrix, one binary per
# provider (GO_AGENT_PLAN.md §12). Each binary links exactly one provider tag
# with the provider and default model embedded via -ldflags.
#
# Usage:
#   scripts/build-matrix.sh [provider ...]
# With no arguments, builds all real providers.
set -euo pipefail

MODULE="github.com/ahhh/fury_agent"
BININFO="${MODULE}/internal/buildinfo"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
VERSION="${VERSION:-dev}"
NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
DIST="${DIST:-dist}"

# Provider -> default model. Override via env, e.g. MODEL_anthropic=...
declare -A MODEL=(
  [anthropic]="${MODEL_anthropic:-claude-opus-4-8}"
  [openai]="${MODEL_openai:-gpt-4.1}"
  [gemini]="${MODEL_gemini:-gemini-2.5-pro}"
)

PROVIDERS=("$@")
if [ ${#PROVIDERS[@]} -eq 0 ]; then
  PROVIDERS=(anthropic openai gemini)
fi

# OS/arch matrix.
PLATFORMS=(
  "linux/amd64" "linux/arm64"
  "darwin/amd64" "darwin/arm64"
  "windows/amd64" "windows/arm64"
)

mkdir -p "${DIST}"

for provider in "${PROVIDERS[@]}"; do
  model="${MODEL[$provider]:-}"
  if [ -z "${model}" ]; then
    echo "no model configured for provider '${provider}'" >&2
    exit 2
  fi
  for platform in "${PLATFORMS[@]}"; do
    os="${platform%/*}"
    arch="${platform#*/}"
    ext=""
    [ "${os}" = "windows" ] && ext=".exe"
    out="${DIST}/agent-${provider}-${os}-${arch}${ext}"
    echo "building ${out} (model=${model})"
    CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" go build -trimpath \
      -tags "provider_${provider}" \
      -ldflags "-s -w \
        -X ${BININFO}.Provider=${provider} \
        -X ${BININFO}.Model=${model} \
        -X ${BININFO}.ProviderTag=provider_${provider} \
        -X ${BININFO}.Version=${VERSION} \
        -X ${BININFO}.Commit=${COMMIT} \
        -X ${BININFO}.Time=${NOW}" \
      -o "${out}" ./cmd/agent
  done
done

echo "done. artifacts in ${DIST}/"
