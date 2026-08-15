[CmdletBinding()]
param(
    [string]$InstallRoot = $(if ($env:CATCLIP_INSTALL_ROOT) {
            $env:CATCLIP_INSTALL_ROOT
        } elseif ($env:LOCALAPPDATA) {
            Join-Path $env:LOCALAPPDATA 'Programs\catclip'
        } else {
            Join-Path $HOME 'AppData\Local\Programs\catclip'
        }),
    [switch]$RemoveConfig
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$BinDir = Join-Path $InstallRoot 'bin'
$ShareDir = Join-Path $InstallRoot 'share\catclip'
$ToolsDir = Join-Path $ShareDir 'bin'
$Target = Join-Path $BinDir 'catclip.exe'
$TreeTarget = Join-Path $BinDir 'catclip-tree.exe'
$VersionFile = Join-Path $ShareDir 'VERSION'
$MethodFile = Join-Path $ShareDir 'INSTALL_METHOD'
$RgFile = Join-Path $ToolsDir 'rg.exe'
$FzfFile = Join-Path $ToolsDir 'fzf.exe'
$ConfigDir = if ($env:XDG_CONFIG_HOME) {
    Join-Path $env:XDG_CONFIG_HOME 'catclip'
} else {
    Join-Path $HOME '.config\catclip'
}

function Get-DefaultInstallRoot {
    if ($env:LOCALAPPDATA) {
        return Join-Path $env:LOCALAPPDATA 'Programs\catclip'
    }
    return Join-Path $HOME 'AppData\Local\Programs\catclip'
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
    $item = Get-Item 'Env:CATCLIP_SKIP_PATH_UPDATE' -ErrorAction SilentlyContinue
    if ($null -eq $item) {
        return $false
    }
    $raw = [string]$item.Value
    $raw = $raw.Trim().ToLowerInvariant()
    return $raw -in @('1', 'true', 'yes')
}

function Remove-UserPathEntry {
    param([string]$Entry)

    $entries = Get-UserPathEntries
    $present = Test-PathEntryPresent $entries $Entry

    $normalizedTarget = Normalize-PathEntry $Entry

    if ($present) {
        $updatedEntries = @(
            $entries | Where-Object {
                -not [string]::Equals((Normalize-PathEntry $_), $normalizedTarget, [System.StringComparison]::OrdinalIgnoreCase)
            }
        )
        [Environment]::SetEnvironmentVariable('Path', [string]::Join(';', $updatedEntries), 'User')
    }

    # Mirror install.ps1: also strip the entry from the current session's
    # $env:Path so a stale reference doesn't outlive the binary it pointed to.
    # No-op if the script ran in a child process.
    $sessionEntries = @($env:Path -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    $filtered = @(
        $sessionEntries | Where-Object {
            -not [string]::Equals((Normalize-PathEntry $_), $normalizedTarget, [System.StringComparison]::OrdinalIgnoreCase)
        }
    )
    if ($filtered.Count -ne $sessionEntries.Count) {
        $env:Path = [string]::Join(';', $filtered)
    }

    return $present
}

function Remove-PathIfPresent {
    param([string]$Path)
    if (Test-Path -LiteralPath $Path) {
        Remove-Item -LiteralPath $Path -Force
    }
}

function Remove-DirIfEmpty {
    param([string]$Path)
    if (-not (Test-Path -LiteralPath $Path -PathType Container)) {
        return
    }
    if ((Get-ChildItem -LiteralPath $Path -Force | Measure-Object).Count -eq 0) {
        Remove-Item -LiteralPath $Path -Force
    }
}

Write-Host 'Uninstalling catclip...'

if (Test-Path -LiteralPath $Target) {
    Write-Host "Removing $Target"
    Remove-PathIfPresent $Target
} else {
    Write-Host "Notice: $Target not found"
}

foreach ($path in @($TreeTarget, $VersionFile, $MethodFile, $RgFile, $FzfFile)) {
    if (Test-Path -LiteralPath $path) {
        Write-Host "Removing $path"
        Remove-PathIfPresent $path
    }
}

Remove-DirIfEmpty $ToolsDir
Remove-DirIfEmpty $ShareDir
Remove-DirIfEmpty (Join-Path $InstallRoot 'share')
Remove-DirIfEmpty $BinDir
Remove-DirIfEmpty $InstallRoot

if (Should-ManageUserPath $InstallRoot) {
    if (Skip-UserPathUpdateRequested) {
        Write-Host 'Note: skipped automatic user PATH cleanup because CATCLIP_SKIP_PATH_UPDATE is set.'
    } elseif (Remove-UserPathEntry $BinDir) {
        Write-Host "Removed $BinDir from your user PATH (current session updated; new sessions will reflect the change automatically)."
    }
}

if (Test-Path -LiteralPath $ConfigDir) {
    if ($RemoveConfig) {
        Remove-Item -LiteralPath $ConfigDir -Recurse -Force
        Write-Host "Removed config at $ConfigDir"
    } else {
        Write-Host "Preserved config at $ConfigDir"
    }
}

Write-Host 'Done.'
