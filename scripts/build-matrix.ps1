# Cross-compile fury_agent for the supported OS/arch matrix, one binary per
# provider (GO_AGENT_PLAN.md §12). Each binary links exactly one provider tag
# with the provider and default model embedded via -ldflags.
#
# Usage:
#   scripts\build-matrix.ps1 [-Providers anthropic,openai,gemini]
param(
  [string[]]$Providers = @('anthropic', 'openai', 'gemini')
)

$ErrorActionPreference = 'Stop'

$Module  = 'github.com/ahhh/fury_agent'
$BinInfo = "$Module/internal/buildinfo"
$Commit  = (git rev-parse --short HEAD 2>$null); if (-not $Commit) { $Commit = 'none' }
$Version = if ($env:VERSION) { $env:VERSION } else { 'dev' }
$Now     = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
$Dist    = if ($env:DIST) { $env:DIST } else { 'dist' }

$Model = @{
  anthropic = if ($env:MODEL_anthropic) { $env:MODEL_anthropic } else { 'claude-opus-4-8' }
  openai    = if ($env:MODEL_openai)    { $env:MODEL_openai }    else { 'gpt-4.1' }
  gemini    = if ($env:MODEL_gemini)    { $env:MODEL_gemini }    else { 'gemini-2.5-pro' }
}

$Platforms = @(
  @{ os = 'linux';   arch = 'amd64' }, @{ os = 'linux';   arch = 'arm64' },
  @{ os = 'darwin';  arch = 'amd64' }, @{ os = 'darwin';  arch = 'arm64' },
  @{ os = 'windows'; arch = 'amd64' }, @{ os = 'windows'; arch = 'arm64' }
)

New-Item -ItemType Directory -Force -Path $Dist | Out-Null

foreach ($provider in $Providers) {
  $mdl = $Model[$provider]
  if (-not $mdl) { throw "no model configured for provider '$provider'" }
  foreach ($p in $Platforms) {
    $ext = if ($p.os -eq 'windows') { '.exe' } else { '' }
    $out = Join-Path $Dist "agent-$provider-$($p.os)-$($p.arch)$ext"
    Write-Host "building $out (model=$mdl)"
    $ldflags = "-s -w " +
      "-X $BinInfo.Provider=$provider " +
      "-X $BinInfo.Model=$mdl " +
      "-X $BinInfo.ProviderTag=provider_$provider " +
      "-X $BinInfo.Version=$Version " +
      "-X $BinInfo.Commit=$Commit " +
      "-X $BinInfo.Time=$Now"
    $env:CGO_ENABLED = '0'; $env:GOOS = $p.os; $env:GOARCH = $p.arch
    go build -trimpath -tags "provider_$provider" -ldflags $ldflags -o $out ./cmd/agent
  }
}

Write-Host "done. artifacts in $Dist/"
