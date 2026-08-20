$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Stop-Install([string]$Message) {
    throw "oscar-corrtest install: $Message"
}

if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    Stop-Install 'the PowerShell installer supports Windows only'
}
$architecture = $env:PROCESSOR_ARCHITEW6432
if (-not $architecture) {
    $architecture = $env:PROCESSOR_ARCHITECTURE
}
if ($architecture -ne 'AMD64') {
    Stop-Install "unsupported Windows architecture: $architecture (amd64 is required)"
}

$repository = 'cmetech/oscar-corrtest'
$releaseBaseUrl = if ($env:OSCAR_CORRTEST_RELEASE_BASE_URL) { $env:OSCAR_CORRTEST_RELEASE_BASE_URL } else { "https://github.com/$repository/releases/download" }
$releaseApiUrl = if ($env:OSCAR_CORRTEST_RELEASE_API_URL) { $env:OSCAR_CORRTEST_RELEASE_API_URL } else { "https://api.github.com/repos/$repository/releases/latest" }
if (-not $env:LOCALAPPDATA) {
    Stop-Install 'LOCALAPPDATA is required for a user-scoped installation'
}
$installDirectory = if ($env:OSCAR_CORRTEST_INSTALL_DIR) { $env:OSCAR_CORRTEST_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'oscar-corrtest\bin' }
if ($installDirectory -notmatch '^(?:[A-Za-z]:[\\/]|\\\\[^\\]+\\[^\\]+)') {
    Stop-Install 'OSCAR_CORRTEST_INSTALL_DIR must be an absolute path'
}
$installDirectory = [IO.Path]::GetFullPath($installDirectory)

function Copy-Download([string]$Uri, [string]$Destination) {
    $parsed = [uri]$Uri
    if ($parsed.IsFile) {
        Copy-Item -LiteralPath $parsed.LocalPath -Destination $Destination
        return
    }
    Invoke-WebRequest -UseBasicParsing -Uri $parsed -OutFile $Destination
}

$work = Join-Path ([IO.Path]::GetTempPath()) ("oscar-corrtest-install-" + [guid]::NewGuid().ToString('N'))
$installTemporary = $null
$installBackup = $null
try {
    New-Item -ItemType Directory -Path $work -Force | Out-Null
    $version = $env:OSCAR_CORRTEST_VERSION
    if (-not $version) {
        $latestPath = Join-Path $work 'latest.json'
        Copy-Download $releaseApiUrl $latestPath
        $latest = Get-Content -LiteralPath $latestPath -Raw | ConvertFrom-Json
        $version = [string]$latest.tag_name
    }
    if ($version -notmatch '^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$') {
        Stop-Install "release version must be an exact semantic tag (vX.Y.Z): $version"
    }

    $assetName = "oscar-corrtest_${version}_windows_amd64.zip"
    $archivePath = Join-Path $work $assetName
    $sumsPath = Join-Path $work 'SHA256SUMS'
    $base = $releaseBaseUrl.TrimEnd('/')
    Copy-Download "$base/$version/$assetName" $archivePath
    Copy-Download "$base/$version/SHA256SUMS" $sumsPath

    $escapedName = [regex]::Escape($assetName)
    $checksumRows = @(Get-Content -LiteralPath $sumsPath | Where-Object { $_ -match "[ *]$escapedName`$" })
    if ($checksumRows.Count -ne 1) {
        Stop-Install "SHA256SUMS must contain exactly one row for $assetName"
    }
    if ($checksumRows[0] -notmatch "^(?<hash>[0-9a-fA-F]{64})  $escapedName`$") {
        Stop-Install "SHA256SUMS contains an invalid row for $assetName"
    }
    $expected = $Matches.hash.ToLowerInvariant()
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        Stop-Install "SHA-256 mismatch for $assetName"
    }

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::OpenRead($archivePath)
    try {
        $binaryCount = 0
        foreach ($entry in $zip.Entries) {
            $entryName = $entry.FullName.Replace('\', '/')
            if ($entryName.StartsWith('/') -or $entryName -match '(^|/)\.\.(/|$)' -or $entryName -notmatch '^oscar-corrtest/') {
                Stop-Install "unsafe ZIP member: $entryName"
            }
            $unixType = (($entry.ExternalAttributes -shr 16) -band 0xF000)
            if ($unixType -eq 0xA000) {
                Stop-Install "ZIP member is a symlink: $entryName"
            }
            if ($entryName -eq 'oscar-corrtest/bin/oscar-corrtest.exe') {
                $binaryCount++
            }
        }
        if ($binaryCount -ne 1) {
            Stop-Install 'ZIP must contain exactly one oscar-corrtest/bin/oscar-corrtest.exe member'
        }
    } finally {
        $zip.Dispose()
    }

    $stage = Join-Path $work 'stage'
    Expand-Archive -LiteralPath $archivePath -DestinationPath $stage
    $stagedBinary = Join-Path $stage 'oscar-corrtest\bin\oscar-corrtest.exe'
    $stagedItem = Get-Item -LiteralPath $stagedBinary
    if ($stagedItem.PSIsContainer -or (($stagedItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0)) {
        Stop-Install 'staged executable is not a regular file'
    }

    New-Item -ItemType Directory -Path $installDirectory -Force | Out-Null
    $destination = Join-Path $installDirectory 'oscar-corrtest.exe'
    $installTemporary = Join-Path $installDirectory ('.oscar-corrtest-' + [guid]::NewGuid().ToString('N') + '.tmp')
    Copy-Item -LiteralPath $stagedBinary -Destination $installTemporary
    if (Test-Path -LiteralPath $destination -PathType Leaf) {
        $installBackup = Join-Path $installDirectory ('.oscar-corrtest-backup-' + [guid]::NewGuid().ToString('N') + '.tmp')
        [IO.File]::Replace($installTemporary, $destination, $installBackup, $true)
        $installTemporary = $null
        Remove-Item -LiteralPath $installBackup -Force
        $installBackup = $null
    } else {
        [IO.File]::Move($installTemporary, $destination)
        $installTemporary = $null
    }

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $pathParts = @($userPath -split ';' | Where-Object { $_ })
    $normalizedInstall = $installDirectory.TrimEnd('\')
    $pathPresent = $false
    foreach ($pathPart in $pathParts) {
        if ($pathPart.TrimEnd('\').Equals($normalizedInstall, [StringComparison]::OrdinalIgnoreCase)) {
            $pathPresent = $true
        }
    }
    if (-not $pathPresent) {
        $updatedPath = if ($userPath) { "$userPath;$installDirectory" } else { $installDirectory }
        [Environment]::SetEnvironmentVariable('Path', $updatedPath, 'User')
    }
    if (-not (($env:Path -split ';') | Where-Object { $_.TrimEnd('\').Equals($normalizedInstall, [StringComparison]::OrdinalIgnoreCase) })) {
        $env:Path = "$installDirectory;$env:Path"
    }

    Write-Output "`nInstalled oscar-corrtest $version at:`n  $destination"
    Write-Output "`nThe installer did not start a service or change corrtest data."
    Write-Output "Start the UI explicitly:`n  & '$destination' serve"
    Write-Output "Then open:`n  http://<server-ip>:8787"
    Write-Output "`nTo run OSCAR tests, provide only an external API key reference:"
    Write-Output "  `$env:OSCAR_API_KEY = '<your-api-key>'"
    Write-Output "  & '$destination' target add --name lab-a --url https://oscar.example/ext/mw --credential-env OSCAR_API_KEY"
} finally {
    if ($installTemporary -and (Test-Path -LiteralPath $installTemporary)) {
        Remove-Item -LiteralPath $installTemporary -Force -ErrorAction SilentlyContinue
    }
    if ($installBackup -and (Test-Path -LiteralPath $installBackup)) {
        Remove-Item -LiteralPath $installBackup -Force -ErrorAction SilentlyContinue
    }
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
