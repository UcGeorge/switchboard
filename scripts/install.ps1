$ErrorActionPreference = 'Stop'
$repo = if ($env:SWITCHBOARD_REPO) { $env:SWITCHBOARD_REPO } else { 'ucgeorge/switchboard' }
$version = if ($env:SWITCHBOARD_VERSION) { $env:SWITCHBOARD_VERSION } else { 'latest' }
$dir = if ($env:SWITCHBOARD_INSTALL_DIR) { $env:SWITCHBOARD_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Switchboard\bin' }
$arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLower()
if ($arch -eq 'x64') { $arch = 'amd64' }
if ($arch -notin @('amd64', 'arm64')) { throw "Unsupported architecture: $arch" }
$base = if ($env:SWITCHBOARD_RELEASE_BASE_URL) { $env:SWITCHBOARD_RELEASE_BASE_URL } else { "https://github.com/$repo/releases" }
if ($version -eq 'latest') { $url = "$base/latest/download" } else {
    if ($version -notmatch '^v?[a-zA-Z0-9.-]+$') { throw 'Invalid version' }
    $url = "$base/download/v$($version.TrimStart('v'))"
}
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Invoke-WebRequest "$url/checksums.txt" -OutFile "$tmp/checksums.txt"
    $matches = @(Get-Content "$tmp/checksums.txt" | Where-Object { $_ -match "^[a-f0-9]{64}\s+switchboard_[a-zA-Z0-9.-]+_windows_$arch\.zip$" })
    if ($matches.Count -ne 1) { throw 'Invalid or ambiguous release manifest' }
    $parts = $matches[0] -split '\s+'
    $asset = $parts[1]
    Invoke-WebRequest "$url/$asset" -OutFile "$tmp/$asset"
    $stream = [System.IO.File]::OpenRead("$tmp/$asset")
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try { $actual = [BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-', '').ToLowerInvariant() }
    finally { $stream.Dispose(); $sha.Dispose() }
    if ($actual -ne $parts[0]) { throw 'Checksum mismatch: nothing installed' }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    [System.IO.Compression.ZipFile]::ExtractToDirectory("$tmp/$asset", "$tmp/unpacked")
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    Copy-Item "$tmp/unpacked/switchboard.exe" "$dir/switchboard.exe" -Force
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($env:SWITCHBOARD_NO_PATH_UPDATE -ne '1' -and $dir -notin ($userPath -split ';')) { [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User') }
    $env:Path = "$dir;$env:Path"
    & "$dir/switchboard.exe" version
    Write-Host "Installed in $dir. New terminals will have switchboard on PATH."
} finally { Remove-Item $tmp -Recurse -Force }
