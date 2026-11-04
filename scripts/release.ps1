# Safe release tagging: refuses to tag unless the repo is clean, synced with
# origin/main, and the tag target is explicitly verified.
#
# Usage:
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/release.ps1 -Tag v0.M4a -Message "M4a: ..."
#
# Why: tagging has been done three times at the wrong commit. Every precondition
# is checked here, and tagging is a separate step from merging.

param(
    [Parameter(Mandatory = $true)][string]$Tag,
    [string]$Message = ""
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

function Fail([string]$reason) {
    Write-Host "REFUSED: $reason" -ForegroundColor Red
    exit 1
}

# 1. Working tree must be clean.
$dirty = git status --porcelain
if ($dirty) { Fail "working tree is not clean:`n$dirty" }

# 2. Local branch must be main.
$branch = (git rev-parse --abbrev-ref HEAD).Trim()
if ($branch -ne 'main') { Fail "current branch is '$branch', expected 'main'" }

# 3. Fetch and require HEAD == origin/main.
git fetch origin
if ($LASTEXITCODE -ne 0) { Fail "git fetch failed (network?)" }
$head = (git rev-parse HEAD).Trim()
$remote = (git rev-parse origin/main).Trim()
if ($head -ne $remote) {
    Fail "HEAD ($($head.Substring(0,7))) != origin/main ($($remote.Substring(0,7))); pull or reset first"
}

# 4. Tag must not already exist.
$existing = git tag --list $Tag
if ($existing) { Fail "tag '$Tag' already exists locally; delete it explicitly if it is wrong" }

# 5. Create annotated tag on the verified commit.
if (-not $Message) { $Message = $Tag }
git tag -a $Tag -m $Message
if ($LASTEXITCODE -ne 0) { Fail "git tag failed" }

$target = (git rev-list -n 1 $Tag).Trim()
if ($target -ne $head) { Fail "tag target $($target.Substring(0,7)) != HEAD $($head.Substring(0,7))" }

# 6. Push and verify the remote resolves to the same commit.
# Verification retries: a flaky network must not report a false failure after a
# successful push (it did once, which is worse than a slow check).
git push origin $Tag
if ($LASTEXITCODE -ne 0) { Fail "git push of tag failed (network?)" }

$remoteCommit = ''
# Peel syntax is required: `git ls-remote --tags origin <pattern>` returns only the
# tag object, never the dereferenced (`^{}`) line. Using the wrong form produced
# false "could not verify" failures on already-successful releases.
$peel = "refs/tags/" + $Tag + "^{}"
for ($attempt = 1; $attempt -le 6; $attempt++) {
    $line = git ls-remote origin $peel 2>$null
    if ($LASTEXITCODE -eq 0 -and $line) { $remoteCommit = ($line -split '\s+')[0]; break }
    Start-Sleep -Seconds 5
}
if (-not $remoteCommit) { Fail "could not verify remote tag after 5 attempts (pushed, but unverified)" }
if ($remoteCommit -ne $head) { Fail "remote tag points to $($remoteCommit.Substring(0,7)), expected $($head.Substring(0,7))" }

Write-Host "tag $Tag -> $($head.Substring(0,7)) verified on origin" -ForegroundColor Green
