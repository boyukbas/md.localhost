<#
.SYNOPSIS
    Removes md.localhost and its file associations.

.DESCRIPTION
    Stops a running server, deletes the registry keys created by install.ps1
    and removes the installed binary. Everything it touches lives under
    HKEY_CURRENT_USER and your own profile.

.PARAMETER InstallDir
    Where the binary was installed. Defaults to
    %LOCALAPPDATA%\Programs\md.localhost.

.PARAMETER KeepFiles
    Remove the associations but leave the installed binary in place.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\uninstall.ps1
#>
[CmdletBinding()]
param(
    [string]   $InstallDir = (Join-Path $env:LOCALAPPDATA 'Programs\md.localhost'),
    [string[]] $Extensions = @('.md', '.markdown', '.mdown', '.mkd'),
    [switch]   $KeepFiles
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$ProgId  = 'mdlocalhost.markdown'
$classes = 'HKCU:\Software\Classes'

function Write-Step { param([string]$Text) Write-Host "  $Text" -ForegroundColor Cyan }

Write-Host ""
Write-Host "md.localhost uninstaller" -ForegroundColor White
Write-Host ""

# Stop the background server so nothing holds the executable open.
$exe = Join-Path $InstallDir 'mdlocalhost.exe'
foreach ($candidate in @($exe, (Join-Path $PSScriptRoot 'mdlocalhost.exe'))) {
    if (Test-Path $candidate) {
        try { & $candidate stop 2>$null | Out-Null } catch { }
    }
}
Get-Process -Name 'mdlocalhost' -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 500

# Desktop integration: the logon entry and the md-localhost: link. Removed
# directly rather than through the executable, which may already be gone.
Remove-ItemProperty -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run' -Name 'md.localhost' -ErrorAction SilentlyContinue
if (Test-Path "$classes\md-localhost") { Remove-Item -Path "$classes\md-localhost" -Recurse -Force }
Write-Step "Removed autostart and the md-localhost: link"

foreach ($ext in $Extensions) {
    $progIds = "$classes\$ext\OpenWithProgids"
    if (Test-Path $progIds) {
        Remove-ItemProperty -Path $progIds -Name $ProgId -ErrorAction SilentlyContinue
    }
    $extKey = "$classes\$ext"
    if (Test-Path $extKey) {
        $default = (Get-Item $extKey).GetValue('')
        if ($default -eq $ProgId) {
            # Clear our claim but leave the key: other handlers may list here.
            Set-ItemProperty -Path $extKey -Name '(Default)' -Value ''
        }
    }
}
Write-Step ("Released " + ($Extensions -join ', '))

foreach ($key in @("$classes\$ProgId", "$classes\Applications\mdlocalhost.exe")) {
    if (Test-Path $key) { Remove-Item -Path $key -Recurse -Force }
}
Write-Step "Removed file type registration"

$lnk = Join-Path ([Environment]::GetFolderPath('Programs')) 'md.localhost.lnk'
if (Test-Path $lnk) { Remove-Item $lnk -Force; Write-Step "Removed Start Menu shortcut" }

$stateDir = Join-Path $env:LOCALAPPDATA 'md.localhost'
if (Test-Path $stateDir) { Remove-Item $stateDir -Recurse -Force; Write-Step "Removed cached state" }

if (-not $KeepFiles -and (Test-Path $InstallDir)) {
    try {
        Remove-Item $InstallDir -Recurse -Force
        Write-Step "Removed $InstallDir"
    } catch {
        Write-Warning "Could not delete $InstallDir - it may still be in use. Delete it manually."
    }
}

if (-not ('MdLocalhost.Shell32Un' -as [type])) {
    Add-Type -Namespace MdLocalhost -Name Shell32Un -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("shell32.dll")]
public static extern void SHChangeNotify(int eventId, uint flags, System.IntPtr item1, System.IntPtr item2);
'@
}
[MdLocalhost.Shell32Un]::SHChangeNotify(0x08000000, 0x0000, [IntPtr]::Zero, [IntPtr]::Zero)

Write-Host ""
Write-Host "Done." -ForegroundColor Green
Write-Host "  If Windows still shows md.localhost as the default for .md, pick a new" -ForegroundColor DarkGray
Write-Host "  default via right-click -> Open with -> Choose another app." -ForegroundColor DarkGray
Write-Host ""
