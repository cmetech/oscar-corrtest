param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$AssetDirectory
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if (-not $IsWindows) {
    throw 'Windows installer smoke must run on Windows'
}
if ($Version -notmatch '^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$') {
    throw "Test version must be an exact semantic tag: $Version"
}

$assetDirectoryPath = (Resolve-Path -LiteralPath $AssetDirectory).Path
$assetName = "oscar-corrtest_${Version}_windows_amd64.zip"
$sourceAsset = Join-Path $assetDirectoryPath $assetName
$sourceSums = Join-Path $assetDirectoryPath 'SHA256SUMS'
if (-not (Test-Path -LiteralPath $sourceAsset -PathType Leaf) -or -not (Test-Path -LiteralPath $sourceSums -PathType Leaf)) {
    throw 'Windows release assets must exist before the installer smoke'
}

$work = Join-Path ([IO.Path]::GetTempPath()) ("oscar-corrtest-install-test-" + [guid]::NewGuid().ToString('N'))
$releaseRoot = Join-Path $work 'releases'
$releaseDirectory = Join-Path $releaseRoot $Version
$localAppData = Join-Path $work 'LocalAppData'
$stateDirectory = Join-Path $localAppData 'oscar-corrtest-state-sentinel'
$installDirectory = Join-Path $localAppData 'oscar-corrtest\bin'
$originalUserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$originalProcessPath = $env:Path

try {
    New-Item -ItemType Directory -Path $releaseDirectory, $stateDirectory -Force | Out-Null
    Copy-Item -LiteralPath $sourceAsset -Destination (Join-Path $releaseDirectory $assetName)
    Copy-Item -LiteralPath $sourceSums -Destination (Join-Path $releaseDirectory 'SHA256SUMS')
    Set-Content -LiteralPath (Join-Path $stateDirectory 'sentinel') -Value 'preserve this state' -NoNewline
    $stateBefore = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $stateDirectory 'sentinel')).Hash

    $env:LOCALAPPDATA = $localAppData
    $env:OSCAR_CORRTEST_VERSION = $Version
    $env:OSCAR_CORRTEST_INSTALL_DIR = $installDirectory
    $env:OSCAR_CORRTEST_RELEASE_BASE_URL = ([uri]$releaseRoot).AbsoluteUri.TrimEnd('/')

    & (Join-Path $PSScriptRoot 'install.ps1')
    $installed = Join-Path $installDirectory 'oscar-corrtest.exe'
    if (-not (Test-Path -LiteralPath $installed -PathType Leaf)) {
        throw 'Installer did not create the Windows executable'
    }
    $versionOutput = & $installed version
    if (($versionOutput -join "`n") -notmatch [regex]::Escape("oscar-corrtest $Version ")) {
        throw "Installed binary reported an unexpected version: $versionOutput"
    }
    $installedBefore = (Get-FileHash -Algorithm SHA256 -LiteralPath $installed).Hash

    & (Join-Path $PSScriptRoot 'install.ps1')
    if ((Get-FileHash -Algorithm SHA256 -LiteralPath $installed).Hash -ne $installedBefore) {
        throw 'Idempotent reinstall changed the Windows executable'
    }
    $installerDebris = @(Get-ChildItem -LiteralPath $installDirectory -Force | Where-Object { $_.Name -like '.oscar-corrtest-*.tmp' })
    if ($installerDebris.Count -ne 0) {
        throw "Idempotent reinstall left installer temporary files: $($installerDebris.Name -join ', ')"
    }

    Add-Content -LiteralPath (Join-Path $releaseDirectory $assetName) -Value 'corrupt' -NoNewline
    $failed = $false
    try {
        & (Join-Path $PSScriptRoot 'install.ps1') *> $null
    } catch {
        $failed = $true
    }
    if (-not $failed) {
        throw 'Installer accepted a corrupt Windows archive'
    }
    if ((Get-FileHash -Algorithm SHA256 -LiteralPath $installed).Hash -ne $installedBefore) {
        throw 'Checksum failure changed the installed Windows executable'
    }
    if ((Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $stateDirectory 'sentinel')).Hash -ne $stateBefore) {
        throw 'Windows installer changed unrelated state'
    }

    Write-Output 'Windows installer smoke passed'
} finally {
    [Environment]::SetEnvironmentVariable('Path', $originalUserPath, 'User')
    $env:Path = $originalProcessPath
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
