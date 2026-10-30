# Install or update the simpleota CLI on Windows.
#
#   irm https://github.com/wassie-dev/simpleOTA-cli/releases/latest/download/install.ps1 | iex
#
# Run it again to update. It downloads the release archive for this machine,
# checks it against the release's checksums.txt, installs simpleota.exe for
# the current user and adds its folder to the user PATH. Settings
# (environment):
#
#   SIMPLEOTA_VERSION       version to install, e.g. 0.1.0 (default: latest)
#   SIMPLEOTA_INSTALL_DIR   where to put simpleota.exe
#                           (default: %LOCALAPPDATA%\Programs\simpleota)
#   SIMPLEOTA_DOWNLOAD_URL  base URL of a mirror of the GitHub release
#                           downloads (https, or http on localhost)
#   SIMPLEOTA_NO_MODIFY_PATH  set to 1 to leave the user PATH alone

function Install-SimpleOTA {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $repo = 'wassie-dev/simpleOTA-cli'

    $base = $env:SIMPLEOTA_DOWNLOAD_URL
    if (-not $base) { $base = "https://github.com/$repo/releases/download" }
    if ($base -notmatch '^https://' -and $base -notmatch '^http://(127\.0\.0\.1|localhost):') {
        throw 'simpleota install: SIMPLEOTA_DOWNLOAD_URL must be https (or http on localhost)'
    }

    $cpu = $env:PROCESSOR_ARCHITEW6432
    if (-not $cpu) { $cpu = $env:PROCESSOR_ARCHITECTURE }
    switch ($cpu) {
        'AMD64' { $arch = 'amd64' }
        'ARM64' { $arch = 'arm64' }
        default { throw "simpleota install: unsupported CPU '$cpu'; simpleota is built for amd64 and arm64" }
    }

    $version = "$env:SIMPLEOTA_VERSION".TrimStart('v')
    if (-not $version) {
        $release = Invoke-RestMethod -UseBasicParsing -Uri "https://api.github.com/repos/$repo/releases/latest"
        $version = "$($release.tag_name)".TrimStart('v')
    }
    if ($version -notmatch '^[0-9][0-9A-Za-z.+-]*$') {
        throw "simpleota install: invalid version '$version'"
    }

    $dir = $env:SIMPLEOTA_INSTALL_DIR
    if (-not $dir) { $dir = Join-Path $env:LOCALAPPDATA 'Programs\simpleota' }
    $target = Join-Path $dir 'simpleota.exe'

    $current = ''
    if (Test-Path $target) {
        try {
            $out = & $target --version 2>$null
            if ("$out" -match '^simpleota version v?(\S+)') { $current = $Matches[1] }
        } catch {
            $current = ''
        }
    }
    if ($current -eq $version) {
        Write-Host "simpleota $version is already installed at $target"
        return
    }

    $archive = "simpleota_${version}_windows_${arch}.zip"
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("simpleota-" + [Guid]::NewGuid())
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        Write-Host "Downloading simpleota $version for windows/$arch"
        Invoke-WebRequest -UseBasicParsing -Uri "$base/v$version/$archive" -OutFile (Join-Path $tmp $archive)
        Invoke-WebRequest -UseBasicParsing -Uri "$base/v$version/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

        $want = ''
        foreach ($line in Get-Content (Join-Path $tmp 'checksums.txt')) {
            $parts = $line -split '\s+'
            if ($parts.Count -ge 2 -and $parts[1] -eq $archive) { $want = $parts[0].ToLower() }
        }
        if (-not $want) { throw "simpleota install: $archive is not listed in checksums.txt" }
        $got = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $tmp $archive)).Hash.ToLower()
        if ($got -ne $want) { throw "simpleota install: checksum mismatch for ${archive}: expected $want, got $got" }

        Expand-Archive -Path (Join-Path $tmp $archive) -DestinationPath (Join-Path $tmp 'x')
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        try {
            Copy-Item -Force (Join-Path $tmp 'x\simpleota.exe') $target
        } catch {
            throw "simpleota install: could not replace $target; close any running simpleota and try again ($_)"
        }
    } finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }

    if ($current) {
        Write-Host "Updated simpleota from $current to $version at $target"
    } else {
        Write-Host "Installed simpleota $version to $target"
    }

    if ($env:SIMPLEOTA_NO_MODIFY_PATH -eq '1') { return }
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @("$userPath" -split ';' | Where-Object { $_ })
    if ($entries -notcontains $dir) {
        [Environment]::SetEnvironmentVariable('Path', (($entries + $dir) -join ';'), 'User')
        $env:Path = "$env:Path;$dir"
        Write-Host "Added $dir to your user PATH. Open a new terminal to use simpleota there."
    }
}

Install-SimpleOTA
