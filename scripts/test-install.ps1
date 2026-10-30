# Test install.ps1 and simpleota update against a goreleaser snapshot served
# from a local mirror.
#
#   goreleaser release --snapshot --clean --skip=publish
#   pwsh scripts/test-install.ps1
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$dist = Join-Path $root 'dist'
$version = (Get-Content (Join-Path $dist 'metadata.json') | ConvertFrom-Json).version
$work = Join-Path ([IO.Path]::GetTempPath()) ("simpleota-test-" + [Guid]::NewGuid())
$www = Join-Path $work "www/v$version"
New-Item -ItemType Directory -Force -Path $www | Out-Null
Copy-Item (Join-Path $dist '*.zip'), (Join-Path $dist 'checksums.txt') $www
$port = 8767
$python = if (Get-Command python3 -ErrorAction SilentlyContinue) { 'python3' } else { 'python' }
$server = Start-Process -PassThru -NoNewWindow -FilePath $python -ArgumentList '-m', 'http.server', "$port", '--bind', '127.0.0.1', '--directory', (Join-Path $work 'www')
$ready = $false
for ($i = 0; $i -lt 60 -and -not $ready; $i++) {
    try {
        Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$port/" | Out-Null
        $ready = $true
    } catch {
        Start-Sleep -Milliseconds 500
    }
}
if (-not $ready) { throw 'FAIL: the local mirror did not start' }

function Fail($msg) { throw "FAIL: $msg" }
function Install($v) {
    $env:SIMPLEOTA_VERSION = $v
    (& ([scriptblock]::Create((Get-Content -Raw (Join-Path $root 'install.ps1')))) 6>&1 | Out-String)
}

try {
    $dir = Join-Path $work 'bin'
    $env:SIMPLEOTA_DOWNLOAD_URL = "http://127.0.0.1:$port"
    $env:SIMPLEOTA_INSTALL_DIR = $dir
    $env:SIMPLEOTA_NO_MODIFY_PATH = '1'
    $exe = Join-Path $dir 'simpleota.exe'

    $out = Install $version
    if ($out -notmatch "Installed simpleota $([regex]::Escape($version))") { Fail "fresh install: $out" }
    if ((& $exe --version) -notmatch [regex]::Escape($version)) { Fail 'installed binary version' }
    Write-Host 'ok   fresh install'

    $out = Install "v$version"
    if ($out -notmatch 'already installed') { Fail "re-run: $out" }
    Write-Host 'ok   re-run is a no-op'

    Set-Content -Path $exe -Value 'not a program'
    $out = Install $version
    if ($out -notmatch "Installed simpleota $([regex]::Escape($version))") { Fail "replace unrunnable binary: $out" }
    if ((& $exe --version) -notmatch [regex]::Escape($version)) { Fail 'replaced binary version' }
    Write-Host 'ok   update replaces a broken binary'

    Push-Location $root
    try {
        go build -ldflags '-X github.com/wassie-dev/simpleOTA-cli/internal/version.Version=0.0.1' -o $exe ./cmd/simpleota
        if ($LASTEXITCODE -ne 0) { Fail 'building the old binary' }
    } finally { Pop-Location }
    $out = (& $exe update --version $version 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0 -or $out -notmatch "Updated simpleota from 0\.0\.1 to $([regex]::Escape($version))") { Fail "self-update: $out" }
    if ((& $exe --version) -notmatch [regex]::Escape($version)) { Fail 'self-updated binary version' }
    Write-Host 'ok   simpleota update replaces its own binary'

    $failed = $false
    try { Install '1.2.3' | Out-Null } catch { $failed = $true }
    if (-not $failed) { Fail 'unknown version accepted' }
    Write-Host 'ok   unknown version refused'

    $env:SIMPLEOTA_DOWNLOAD_URL = 'http://example.com'
    $failed = $false
    try { Install $version | Out-Null } catch { $failed = $_.Exception.Message -match 'must be https' }
    if (-not $failed) { Fail 'plain http mirror accepted' }
    Write-Host 'ok   plain http mirror refused'
} finally {
    Stop-Process -Id $server.Id -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
