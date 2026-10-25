<#
.SYNOPSIS
    Installs md.localhost and makes Windows open .md files in your browser.

.DESCRIPTION
    Copies mdlocalhost.exe into your per-user programs folder, registers a
    file type for Markdown, and points the .md family of extensions at it.
    Everything is written under HKEY_CURRENT_USER, so no administrator rights
    are needed and nothing outside your profile is touched.

.PARAMETER InstallDir
    Where to place the binary. Defaults to
    %LOCALAPPDATA%\Programs\md.localhost.

.PARAMETER InPlace
    Register the executable where it already sits instead of copying it.
    Handy while developing; do not move the folder afterwards.

.PARAMETER Extensions
    Which extensions to claim. Defaults to .md, .markdown, .mdown, .mkd.

.PARAMETER StartMenu
    Add a Start Menu shortcut that opens the "recently viewed" page.

.PARAMETER AutoStart
    Keep the viewer's server running from logon onwards, so an installed app
    pinned to the taskbar always has something to open.

.PARAMETER HostsEntry
    Also add "127.0.0.1 md.localhost" to the system hosts file. Only needed
    for browsers that do not resolve *.localhost themselves; requires an
    elevated PowerShell session.

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\install.ps1
#>
[CmdletBinding()]
param(
    [string]   $InstallDir = (Join-Path $env:LOCALAPPDATA 'Programs\md.localhost'),
    [switch]   $InPlace,
    [string[]] $Extensions = @('.md', '.markdown', '.mdown', '.mkd'),
    [switch]   $StartMenu,
    [switch]   $AutoStart,
    [switch]   $HostsEntry
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$ProgId    = 'mdlocalhost.markdown'
$AppName   = 'md.localhost'
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

function Write-Step { param([string]$Text) Write-Host "  $Text" -ForegroundColor Cyan }
function Write-Note { param([string]$Text) Write-Host "  $Text" -ForegroundColor DarkGray }

Write-Host ""
Write-Host "md.localhost installer" -ForegroundColor White
Write-Host ""

# ---------------------------------------------------------------- binary

$sourceExe = Join-Path $ScriptDir 'mdlocalhost.exe'
$sourceIco = Join-Path $ScriptDir 'md.ico'
if (-not (Test-Path $sourceExe)) {
    throw "mdlocalhost.exe was not found next to this script ($ScriptDir). Build it first with build.ps1, or download a release."
}

if ($InPlace) {
    $exe = (Resolve-Path $sourceExe).Path
    $ico = if (Test-Path $sourceIco) { (Resolve-Path $sourceIco).Path } else { $exe }
    Write-Step "Registering in place: $exe"
} else {
    if (-not (Test-Path $InstallDir)) { New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null }
    $exe = Join-Path $InstallDir 'mdlocalhost.exe'
    $ico = Join-Path $InstallDir 'md.ico'

    # A running server holds the file open; ask it to stop before overwriting.
    if (Test-Path $exe) {
        try { & $exe stop 2>$null | Out-Null; Start-Sleep -Milliseconds 600 } catch { }
    }
    Copy-Item $sourceExe $exe -Force
    if (Test-Path $sourceIco) { Copy-Item $sourceIco $ico -Force } else { $ico = $exe }
    Write-Step "Installed to $InstallDir"
}

# ---------------------------------------------------------------- prog id

function Set-Default {
    param([string]$Path, [string]$Value)
    if (-not (Test-Path $Path)) { New-Item -Path $Path -Force | Out-Null }
    Set-ItemProperty -Path $Path -Name '(Default)' -Value $Value
}

$classes = 'HKCU:\Software\Classes'

Set-Default "$classes\$ProgId"                       'Markdown Document'
Set-Default "$classes\$ProgId\DefaultIcon"           "`"$ico`",0"
Set-Default "$classes\$ProgId\shell\open"            '&View in browser'
Set-Default "$classes\$ProgId\shell\open\command"    "`"$exe`" open `"%1`""
Set-ItemProperty -Path "$classes\$ProgId" -Name 'FriendlyTypeName' -Value 'Markdown Document'
Set-ItemProperty -Path "$classes\$ProgId" -Name 'AlwaysShowExt' -Value ''

# A second verb, so the raw text is still one right-click away.
Set-Default "$classes\$ProgId\shell\edit"            'Edit in Notepad'
Set-Default "$classes\$ProgId\shell\edit\command"    "notepad.exe `"%1`""

# Make the app appear under "Open with" even where it is not the default.
$appKey = "$classes\Applications\mdlocalhost.exe"
Set-Default "$appKey\shell\open\command" "`"$exe`" open `"%1`""
Set-ItemProperty -Path $appKey -Name 'FriendlyAppName' -Value $AppName
if (-not (Test-Path "$appKey\SupportedTypes")) { New-Item -Path "$appKey\SupportedTypes" -Force | Out-Null }
foreach ($ext in $Extensions) {
    Set-ItemProperty -Path "$appKey\SupportedTypes" -Name $ext -Value ''
}

Write-Step "Registered file type '$ProgId'"

# ---------------------------------------------------------------- extensions

$needsManual = @()

foreach ($ext in $Extensions) {
    $extKey = "$classes\$ext"
    if (-not (Test-Path $extKey)) { New-Item -Path $extKey -Force | Out-Null }

    $progIds = "$extKey\OpenWithProgids"
    if (-not (Test-Path $progIds)) { New-Item -Path $progIds -Force | Out-Null }
    # Explorer only cares that the value *name* exists; an empty string is
    # accepted everywhere, while an empty REG_NONE trips some PowerShell hosts.
    Set-ItemProperty -Path $progIds -Name $ProgId -Value '' -Type String

    # Windows 10/11 protect the *default* handler with a signed UserChoice key
    # that applications are not permitted to write. Claim the extension only
    # when the user has not already chosen something.
    $userChoice = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Explorer\FileExts\$ext\UserChoice"
    if (Test-Path $userChoice) {
        $choice  = Get-ItemProperty -Path $userChoice -ErrorAction SilentlyContinue
        $current = if ($choice -and $choice.PSObject.Properties['ProgId']) { $choice.ProgId } else { $null }
        if ($current -ne $ProgId) { $needsManual += $ext }
    } else {
        Set-Default $extKey $ProgId
    }
}

Write-Step ("Claimed " + ($Extensions -join ', '))

# ---------------------------------------------------------------- extras

if ($StartMenu) {
    $programs = [Environment]::GetFolderPath('Programs')
    $lnk = Join-Path $programs 'md.localhost.lnk'
    $shell = New-Object -ComObject WScript.Shell
    $sc = $shell.CreateShortcut($lnk)
    $sc.TargetPath       = $exe
    $sc.Arguments        = 'home'
    $sc.IconLocation     = "$ico,0"
    $sc.Description      = 'Markdown files you have viewed recently'
    $sc.WorkingDirectory = Split-Path -Parent $exe
    $sc.Save()
    Write-Step "Start Menu shortcut created"
}

# The offline page uses this link to start the server again when a pinned
# taskbar icon is clicked and nothing is listening.
try {
    & $exe protocol on | Out-Null
    Write-Step "Registered the md-localhost: link"
} catch {
    Write-Warning "Could not register the md-localhost: link: $_"
}

if ($AutoStart) {
    try {
        & $exe autostart on | Out-Null
        Write-Step "Server will start at logon"
    } catch {
        Write-Warning "Could not set autostart: $_"
    }
}

if ($HostsEntry) {
    $hostsFile = Join-Path $env:SystemRoot 'System32\drivers\etc\hosts'
    try {
        $lines = @(Get-Content $hostsFile -ErrorAction Stop)
        # -match on an array filters it, so test for "no matching lines".
        if (-not ($lines -match '^\s*[^#].*\bmd\.localhost\b')) {
            Add-Content -Path $hostsFile -Value "`r`n127.0.0.1`tmd.localhost" -ErrorAction Stop
            Write-Step "Added md.localhost to the hosts file"
        } else {
            Write-Note "hosts file already mentions md.localhost"
        }
    } catch {
        Write-Warning "Could not edit the hosts file (run PowerShell as Administrator to use -HostsEntry). Most browsers resolve *.localhost on their own, so this is usually unnecessary."
    }
}

# Tell Explorer that file associations changed, so icons refresh immediately.
if (-not ('MdLocalhost.Shell32' -as [type])) {
    Add-Type -Namespace MdLocalhost -Name Shell32 -MemberDefinition @'
[System.Runtime.InteropServices.DllImport("shell32.dll")]
public static extern void SHChangeNotify(int eventId, uint flags, System.IntPtr item1, System.IntPtr item2);
'@
}
[MdLocalhost.Shell32]::SHChangeNotify(0x08000000, 0x0000, [IntPtr]::Zero, [IntPtr]::Zero)

# ---------------------------------------------------------------- report

Write-Host ""
Write-Host "Done." -ForegroundColor Green
Write-Note "Double-click any .md file and it opens at http://md.localhost/"
Write-Note "Commands: mdlocalhost home | status | stop | autostart on|off"
Write-Note "To pin it to the taskbar: open the viewer, then in Chrome's menu choose"
Write-Note "  Cast, save and share > Install page as app, and pin the app that appears."


if ($needsManual.Count -gt 0) {
    Write-Host ""
    Write-Warning ("Windows already has a default app for: " + ($needsManual -join ', '))
    Write-Host "  Windows does not let a program change that setting for you. To finish:" -ForegroundColor Yellow
    Write-Host "    right-click a .md file -> Open with -> Choose another app" -ForegroundColor Yellow
    Write-Host "    -> md.localhost -> tick 'Always use this app'." -ForegroundColor Yellow
}
Write-Host ""
