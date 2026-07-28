[CmdletBinding()]
param(
    [string]$InstallRoot = $(if ($env:CATCLIP_INSTALL_ROOT) {
            $env:CATCLIP_INSTALL_ROOT
        } elseif ($env:LOCALAPPDATA) {
            Join-Path $env:LOCALAPPDATA 'Programs\catclip'
        } else {
            Join-Path $HOME 'AppData\Local\Programs\catclip'
        }),
    [string]$Version = $(if ($env:CATCLIP_INSTALL_VERSION) { $env:CATCLIP_INSTALL_VERSION } else { 'latest' }),
    [string]$ReleaseBaseUrl = $(if ($env:CATCLIP_RELEASE_BASE_URL) { $env:CATCLIP_RELEASE_BASE_URL } else { 'https://github.com/tigreau/catclip/releases' }),
    [switch]$SkipVerify
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Suppress Invoke-WebRequest's progress bar — it redraws on every byte and
# can slow downloads 10-50x in interactive PowerShell sessions. The bar
# carries no useful information for a scripted installer.
$ProgressPreference = 'SilentlyContinue'

$ProgramName = 'catclip'
$TreeProgramName = 'catclip-tree'
$BinDir = Join-Path $InstallRoot 'bin'
$ShareDir = Join-Path $InstallRoot 'share\catclip'
$ToolsDir = Join-Path $ShareDir 'bin'
$ChecksumsName = 'checksums.txt'

function Fail {
    param([string]$Message)
    throw $Message
}

function Note {
    param([string]$Message)
    Write-Host "Note: $Message"
}

function Write-GettingStarted {
    Write-Host ''
    if (Test-Path Env:NO_COLOR) {
        Write-Host 'Important: run catclip from inside the project you want to inspect.'
        Write-Host ''
        Write-Host '  cd /path/to/project'
        Write-Host '  catclip --help'
        Write-Host ''
        return
    }

    Write-Host 'Important:' -ForegroundColor Yellow -NoNewline
    Write-Host ' run catclip from inside the project you want to inspect.'
    Write-Host ''
    Write-Host '  cd /path/to/project' -ForegroundColor Cyan
    Write-Host '  catclip --help' -ForegroundColor Cyan
    Write-Host ''
}

function Get-DefaultInstallRoot {
    if ($env:LOCALAPPDATA) {
        return Join-Path $env:LOCALAPPDATA 'Programs\catclip'
    }
    return Join-Path $HOME 'AppData\Local\Programs\catclip'
}

function Get-EnvValue {
    param([string]$Name)
    $item = Get-Item "Env:$Name" -ErrorAction SilentlyContinue
    if ($null -eq $item) {
        return ''
    }
    return [string]$item.Value
}

function Get-RequiredGoVersion {
    param([string]$SourceDir)
    $goMod = Join-Path $SourceDir 'go.mod'
    if (-not (Test-Path -LiteralPath $goMod)) {
        return ''
    }
    foreach ($line in Get-Content -LiteralPath $goMod) {
        if ($line -match '^\s*go\s+([0-9]+\.[0-9]+)') {
            return $Matches[1]
        }
    }
    return ''
}

function Normalize-PathEntry {
    param([string]$Path)

    if ([string]::IsNullOrWhiteSpace($Path)) {
        return ''
    }

    $normalized = $Path.Trim()
    while ($normalized.Length -gt 3 -and ($normalized.EndsWith('\') -or $normalized.EndsWith('/'))) {
        $normalized = $normalized.Substring(0, $normalized.Length - 1)
    }
    return $normalized
}

function Test-PathEntryPresent {
    param(
        [string[]]$Entries,
        [string]$Target
    )

    $normalizedTarget = Normalize-PathEntry $Target
    foreach ($entry in $Entries) {
        if ([string]::Equals((Normalize-PathEntry $entry), $normalizedTarget, [System.StringComparison]::OrdinalIgnoreCase)) {
            return $true
        }
    }
    return $false
}

function Get-UserPathEntries {
    $raw = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ([string]::IsNullOrWhiteSpace($raw)) {
        return @()
    }
    return @($raw -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
}

function Should-ManageUserPath {
    param([string]$Root)

    return [string]::Equals(
        (Normalize-PathEntry $Root),
        (Normalize-PathEntry (Get-DefaultInstallRoot)),
        [System.StringComparison]::OrdinalIgnoreCase
    )
}

function Skip-UserPathUpdateRequested {
    $raw = (Get-EnvValue 'CATCLIP_SKIP_PATH_UPDATE').Trim().ToLowerInvariant()
    return $raw -in @('1', 'true', 'yes')
}

function Add-UserPathEntry {
    param([string]$Entry)

    $entries = Get-UserPathEntries
    $alreadyInRegistry = Test-PathEntryPresent $entries $Entry

    if (-not $alreadyInRegistry) {
        $updatedEntries = @($entries + (Normalize-PathEntry $Entry))
        [Environment]::SetEnvironmentVariable('Path', [string]::Join(';', $updatedEntries), 'User')
    }

    # Also update the current session's $env:Path so `catclip --version` works
    # immediately after `irm ... | iex` finishes — matching the macOS/Linux
    # install.sh experience where /usr/local/bin is already on PATH. This is a
    # no-op when the script ran in a child process (e.g. `powershell -Command`),
    # since the change dies with that process.
    $sessionEntries = @($env:Path -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if (-not (Test-PathEntryPresent $sessionEntries $Entry)) {
        $env:Path = (Normalize-PathEntry $Entry) + ';' + $env:Path
    }

    if ($alreadyInRegistry) {
        return 'already'
    }
    return 'added'
}

function Test-GoVersionGte {
    param(
        [string]$Found,
        [string]$Required
    )

    $foundParts = ($Found -replace '[^0-9\.].*$', '').Split('.')
    $requiredParts = ($Required -replace '[^0-9\.].*$', '').Split('.')
    if ($foundParts.Length -lt 2 -or $requiredParts.Length -lt 2) {
        return $true
    }

    $foundMajor = [int]$foundParts[0]
    $foundMinor = [int]$foundParts[1]
    $requiredMajor = [int]$requiredParts[0]
    $requiredMinor = [int]$requiredParts[1]

    if ($foundMajor -gt $requiredMajor) {
        return $true
    }
    if ($foundMajor -lt $requiredMajor) {
        return $false
    }
    return $foundMinor -ge $requiredMinor
}

function Resolve-CommandPath {
    param([string]$Name)
    $command = Get-Command $Name -ErrorAction Stop
    if ($command.Path) {
        return $command.Path
    }
    if ($command.Source) {
        return $command.Source
    }
    Fail "could not resolve executable path for $Name"
}

function Find-LocalSourceDir {
    param([string]$ScriptPath)
    if ([string]::IsNullOrWhiteSpace($ScriptPath)) {
        return $null
    }
    $dir = Split-Path -Parent $ScriptPath
    if (-not (Test-Path -LiteralPath $dir)) {
        return $null
    }
    if ((Test-Path -LiteralPath (Join-Path $dir 'go.mod')) -and
        (Test-Path -LiteralPath (Join-Path $dir 'main.go')) -and
        (Test-Path -LiteralPath (Join-Path $dir 'VERSION'))) {
        return (Resolve-Path -LiteralPath $dir).Path
    }
    return $null
}

function Find-LocalReleaseDir {
    param([string]$ScriptPath)
    if ([string]::IsNullOrWhiteSpace($ScriptPath)) {
        return $null
    }
    $dir = Split-Path -Parent $ScriptPath
    if (-not (Test-Path -LiteralPath $dir)) {
        return $null
    }
    if ((Test-Path -LiteralPath (Join-Path $dir 'VERSION')) -and
        (Test-Path -LiteralPath (Join-Path $dir 'catclip.exe')) -and
        (Test-Path -LiteralPath (Join-Path $dir 'bin\rg.exe')) -and
        (Test-Path -LiteralPath (Join-Path $dir 'bin\fzf.exe'))) {
        return (Resolve-Path -LiteralPath $dir).Path
    }
    return $null
}

function Resolve-BundledToolSource {
    param(
        [string]$EnvVar,
        [string]$ToolName
    )

    $override = (Get-EnvValue $EnvVar).Trim()
    if ($override) {
        if ([System.IO.Path]::IsPathRooted($override) -or $override.Contains('\') -or $override.Contains('/')) {
            if (-not (Test-Path -LiteralPath $override -PathType Leaf)) {
                Fail "$ToolName override at $override is not executable"
            }
            return (Resolve-Path -LiteralPath $override).Path
        }
        return Resolve-CommandPath $override
    }

    try {
        return Resolve-CommandPath $ToolName
    } catch {
        Fail "'$ToolName' is required for Windows source installs because catclip packages a private bundled copy with every install.`nInstall $ToolName first, or set $EnvVar to an executable path."
    }
}

function Ensure-GoForSourceBuild {
    param([string]$SourceDir)

    try {
        $null = Resolve-CommandPath 'go'
    } catch {
        Fail "Go is required when installing from a source checkout.`nUse a published release bundle for a normal Windows install."
    }

    $required = Get-RequiredGoVersion $SourceDir
    if (-not $required) {
        return
    }

    $found = (& go env GOVERSION).Trim()
    $found = $found.TrimStart('g', 'o')
    if (-not (Test-GoVersionGte $found $required)) {
        Fail "Go $required or newer is required to build this checkout.`nFound Go $found.`nUpgrade Go, or use the published Windows release installer instead of building locally."
    }
}

function Ensure-SourceFzfCompatible {
    param([string]$FzfBin)

    "a`n" | & $FzfBin --info=inline-right --filter a *> $null
    if ($LASTEXITCODE -ne 0) {
        Fail "Your local fzf is too old for catclip source installs.`ncatclip's current picker UI requires an fzf build that supports --info=inline-right.`nUpgrade fzf to >= v0.71.0, or use the published Windows release installer instead of building locally."
    }

    "a`n" | & $FzfBin --bind 'multi:refresh-preview' --filter a *> $null
    if ($LASTEXITCODE -ne 0) {
        Fail "Your local fzf is too old for catclip source installs.`ncatclip's multi-select behavior requires an fzf build that supports multi bind events.`nUpgrade fzf to >= v0.71.0, or use the published Windows release installer instead of building locally."
    }
}

function Ensure-SourceRgCompatible {
    param(
        [string]$RgBin,
        [string]$SourceDir
    )

    & $RgBin --files --hidden -0 $SourceDir *> $null
    if ($LASTEXITCODE -ne 0) {
        Fail "Your local ripgrep is too old for catclip source installs.`ncatclip's current file discovery requires an rg build that supports --files with -0 output.`nUpgrade ripgrep to >= v14.1.1, or use the published Windows release installer instead of building locally."
    }

    $tmpFile = New-TemporaryFile
    try {
        [System.IO.File]::WriteAllText($tmpFile.FullName, "catclip-rg-check`n", [System.Text.Encoding]::ASCII)
        & $RgBin --color=never --no-messages --files-with-matches -0 -m 1 -e 'catclip-rg-check' -- $tmpFile.FullName *> $null
        if ($LASTEXITCODE -ne 0) {
            Fail "Your local ripgrep is too old for catclip source installs.`ncatclip's current content filtering requires an rg build that supports --files-with-matches with -0 output.`nUpgrade ripgrep to >= v14.1.1, or use the published Windows release installer instead of building locally."
        }

        & $RgBin --pcre2 -e 'a' $tmpFile.FullName *> $null
        if ($LASTEXITCODE -eq 2) {
            Fail "Your local ripgrep is too old for catclip source installs.`ncatclip requires a ripgrep build with PCRE2 support.`nUpgrade ripgrep to >= v14.1.1, or use the published Windows release installer instead of building locally."
        }
    } finally {
        Remove-Item -LiteralPath $tmpFile.FullName -Force -ErrorAction SilentlyContinue
    }
}

function Build-FromSourceCheckout {
    param(
        [string]$SourceDir,
        [string]$BinaryFile
    )

    Push-Location $SourceDir
    try {
        & go build -trimpath -o $BinaryFile ./cmd/catclip
        if ($LASTEXITCODE -ne 0) {
            Fail 'failed to build catclip from the local source checkout'
        }
    } finally {
        Pop-Location
    }
}

function Get-InstallArch {
    $archCandidates = @()

    if ($env:PROCESSOR_ARCHITEW6432) {
        $archCandidates += [string]$env:PROCESSOR_ARCHITEW6432
    }
    if ($env:PROCESSOR_ARCHITECTURE) {
        $archCandidates += [string]$env:PROCESSOR_ARCHITECTURE
    }

    try {
        if ('System.Runtime.InteropServices.RuntimeInformation' -as [type]) {
            $archCandidates += [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
        }
    } catch {
    }

    foreach ($candidate in $archCandidates) {
        switch ($candidate.Trim().ToLowerInvariant()) {
            'amd64' { return 'amd64' }
            'x64' { return 'amd64' }
            'arm64' { return 'arm64' }
        }
    }

    Fail "unsupported architecture: PROCESSOR_ARCHITECTURE=$env:PROCESSOR_ARCHITECTURE PROCESSOR_ARCHITEW6432=$env:PROCESSOR_ARCHITEW6432"
}

function Build-ReleaseUrl {
    param([string]$Asset)
    if ($Version -eq 'latest') {
        return "$ReleaseBaseUrl/latest/download/$Asset"
    }
    return "$ReleaseBaseUrl/download/$Version/$Asset"
}

function Download-File {
    param(
        [string]$Url,
        [string]$Destination
    )

    try {
        Invoke-WebRequest -Uri $Url -OutFile $Destination
    } catch {
        Remove-Item -LiteralPath $Destination -Force -ErrorAction SilentlyContinue
        Fail "failed to download $Url`nThe requested catclip release bundle may not be published yet.`nTry again later, or run .\install.ps1 from a cloned checkout."
    }
}

function Try-DownloadFile {
    param(
        [string]$Url,
        [string]$Destination
    )

    try {
        Invoke-WebRequest -Uri $Url -OutFile $Destination
        return $true
    } catch {
        Remove-Item -LiteralPath $Destination -Force -ErrorAction SilentlyContinue
        return $false
    }
}

function Verify-Checksum {
    param(
        [string]$AssetName,
        [string]$ArchivePath,
        [string]$ChecksumsPath
    )

    if (-not (Test-Path -LiteralPath $ChecksumsPath)) {
        Write-Warning 'checksums file missing; skipping verification.'
        return
    }
    if (-not (Get-Command Get-FileHash -ErrorAction SilentlyContinue)) {
        Write-Warning 'Get-FileHash not available; skipping verification.'
        return
    }

    $pattern = '^(?<hash>[0-9A-Fa-f]{64})\s+\*?(?<asset>' + [regex]::Escape($AssetName) + ')$'
    $expected = $null
    foreach ($line in Get-Content -LiteralPath $ChecksumsPath) {
        if ($line -match $pattern) {
            $expected = $Matches['hash'].ToLowerInvariant()
            break
        }
    }
    if (-not $expected) {
        Write-Warning "no checksum entry found for $AssetName; skipping verification."
        return
    }

    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $ArchivePath).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        Fail "checksum verification failed for $AssetName"
    }
}

function Install-File {
    param(
        [string]$Source,
        [string]$Destination
    )

    $destDir = Split-Path -Parent $Destination
    if ($destDir) {
        New-Item -ItemType Directory -Force -Path $destDir | Out-Null
    }
    if (-not (Test-Path -LiteralPath $Source -PathType Leaf)) {
        Fail "install source file not found: $Source"
    }

    $isExecutable = [System.IO.Path]::GetExtension($Destination) -ieq '.exe'
    $maxAttempts = $(if ($isExecutable) { 20 } else { 1 })
    for ($attempt = 1; $attempt -le $maxAttempts; $attempt++) {
        try {
            Copy-Item -LiteralPath $Source -Destination $Destination -Force -ErrorAction Stop
            return
        } catch {
            if ($attempt -lt $maxAttempts) {
                Start-Sleep -Milliseconds 250
                continue
            }

            if ($isExecutable) {
                $leaf = Split-Path -Leaf $Destination
                Fail @"
could not replace '$Destination' after waiting for Windows to release it.

'$leaf' is still in use or access was denied. Close any running catclip
picker or command, then check for leftover processes:

  Get-Process catclip, fzf, rg -ErrorAction SilentlyContinue

If a catclip process is stuck, stop it and run the installer again:

  Get-Process catclip -ErrorAction SilentlyContinue | Stop-Process -Force
  irm https://raw.githubusercontent.com/tigreau/catclip/main/install.ps1 | iex

If no related process is running, security software may still be scanning the
file. Wait a few seconds and retry.
"@
            }
            throw
        }
    }
}

function Remove-FileIfExists {
    param([string]$Path)

    if (Test-Path -LiteralPath $Path) {
        Remove-Item -LiteralPath $Path -Force
    }
}

# When the script is invoked via `irm ... | iex`, $MyInvocation.MyCommand is
# an InternalCommand with no Path property — under Set-StrictMode -Version
# Latest, dotting into it throws PropertyNotFoundStrict. Guard the lookup so
# the piped-from-the-web entry point works the same as a local .\install.ps1.
$scriptPath = ''
if ($MyInvocation.MyCommand.PSObject.Properties.Name -contains 'Path') {
    $scriptPath = [string]$MyInvocation.MyCommand.Path
}
$sourceDir = Find-LocalSourceDir $scriptPath
$releaseDir = $null
if (-not $sourceDir) {
    $releaseDir = Find-LocalReleaseDir $scriptPath
}

$archName = Get-InstallArch
$assetName = "${ProgramName}_windows_${archName}.zip"

Write-Host 'Installing catclip...'
Write-Host "Target:   windows/$archName"

if (Test-Path -LiteralPath (Join-Path $BinDir "$ProgramName.exe")) {
    Note "Existing catclip installation detected at $(Join-Path $BinDir "$ProgramName.exe")."
}

$tmpRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("catclip-install-" + [System.Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $tmpRoot | Out-Null

try {
    if ($sourceDir) {
        Ensure-GoForSourceBuild $sourceDir

        Write-Host "Source:   $sourceDir"
        Note 'Building from your local checkout so the installed binary matches the code you have checked out.'
        Note 'Windows source installs are a developer path. Normal users should use the published release bundle.'
        Note 'Source installs require local Go, rg, and fzf once at install time so catclip can bundle private copies into the final install.'

        $versionFile = Join-Path $sourceDir 'VERSION'
        $binaryFile = Join-Path $tmpRoot "$ProgramName.exe"
        $rgFile = Join-Path $tmpRoot 'rg.exe'
        $fzfFile = Join-Path $tmpRoot 'fzf.exe'

        $versionText = (Get-Content -LiteralPath $versionFile | Select-Object -First 1).Trim()
        if (-not $versionText) {
            Fail 'VERSION file is empty'
        }

        $rgSource = Resolve-BundledToolSource 'CATCLIP_RG' 'rg'
        $fzfSource = Resolve-BundledToolSource 'CATCLIP_FZF' 'fzf'
        Ensure-SourceRgCompatible $rgSource $sourceDir
        Ensure-SourceFzfCompatible $fzfSource

        Copy-Item -LiteralPath $rgSource -Destination $rgFile -Force
        Copy-Item -LiteralPath $fzfSource -Destination $fzfFile -Force

        Write-Host "Building $ProgramName from source"
        Build-FromSourceCheckout $sourceDir $binaryFile
    } else {
        $archivePath = Join-Path $tmpRoot $assetName
        $checksumsPath = Join-Path $tmpRoot $ChecksumsName

        if ($releaseDir) {
            Write-Host "Source:   $releaseDir"
            Note 'Installing from a local Windows release bundle directory.'
            $versionFile = Join-Path $releaseDir 'VERSION'
            $binaryFile = Join-Path $releaseDir "$ProgramName.exe"
            $rgFile = Join-Path $releaseDir 'bin\rg.exe'
            $fzfFile = Join-Path $releaseDir 'bin\fzf.exe'
            $versionText = (Get-Content -LiteralPath $versionFile | Select-Object -First 1).Trim()
            if (-not $versionText) {
                Fail 'VERSION file is empty'
            }
        } else {
            Write-Host "Downloading $assetName"
            Download-File (Build-ReleaseUrl $assetName) $archivePath

            if (-not $SkipVerify) {
                Write-Host "Downloading $ChecksumsName"
                if (Try-DownloadFile (Build-ReleaseUrl $ChecksumsName) $checksumsPath) {
                    Write-Host 'Verifying checksum...'
                    Verify-Checksum $assetName $archivePath $checksumsPath
                } else {
                    Write-Warning "failed to download $(Build-ReleaseUrl $ChecksumsName); skipping verification."
                }
            }

            Expand-Archive -LiteralPath $archivePath -DestinationPath $tmpRoot -Force

            $versionFile = Join-Path $tmpRoot 'VERSION'
            $binaryFile = Join-Path $tmpRoot "$ProgramName.exe"
            $rgFile = Join-Path $tmpRoot 'bin\rg.exe'
            $fzfFile = Join-Path $tmpRoot 'bin\fzf.exe'

            foreach ($requiredPath in @($versionFile, $binaryFile, $rgFile, $fzfFile)) {
                if (-not (Test-Path -LiteralPath $requiredPath)) {
                    Fail "release archive is missing $(Split-Path -Leaf $requiredPath)"
                }
            }

            $versionText = (Get-Content -LiteralPath $versionFile | Select-Object -First 1).Trim()
            if (-not $versionText) {
                Fail 'VERSION file is empty'
            }
        }
    }

    Install-File $binaryFile (Join-Path $BinDir "$ProgramName.exe")
    Remove-FileIfExists (Join-Path $BinDir "$TreeProgramName.exe")
    Install-File $versionFile (Join-Path $ShareDir 'VERSION')
    Install-File $rgFile (Join-Path $ToolsDir 'rg.exe')
    Install-File $fzfFile (Join-Path $ToolsDir 'fzf.exe')

    Write-Host 'Done.'
    Write-Host "  Binary:  $(Join-Path $BinDir "$ProgramName.exe")"
    Write-Host "  Version: $versionText"
    Write-Host "  rg:      $(Join-Path $ToolsDir 'rg.exe')"
    Write-Host "  fzf:     $(Join-Path $ToolsDir 'fzf.exe')"
    Write-Host '  Config:  ~/.config/catclip/.hiss'

    if (-not (Should-ManageUserPath $InstallRoot)) {
        Note "Install root is custom; add $BinDir to your user PATH manually if you want to run catclip without the full path."
    } elseif (Skip-UserPathUpdateRequested) {
        Note "Skipped automatic user PATH update because CATCLIP_SKIP_PATH_UPDATE is set."
    } else {
        $pathStatus = Add-UserPathEntry $BinDir
        switch ($pathStatus) {
            'added' {
                Note "Added $BinDir to your user PATH (current session updated; new sessions inherit it from the registry)."
            }
            'already' {
                Note "$BinDir is already on your user PATH; current session refreshed."
            }
        }
    }

    Write-GettingStarted
} finally {
    Remove-Item -LiteralPath $tmpRoot -Recurse -Force -ErrorAction SilentlyContinue
}
