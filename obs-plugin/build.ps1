param(
    [string]$Go = 'go',
    [string]$Clang,
    [string]$ObsSdk,
    [string]$ObsBin = 'C:\Program Files\obs-studio\bin\64bit',
    [string]$Version = '0.1.0-preview.1',
    [switch]$PluginOnly
)
$ErrorActionPreference = 'Stop'
if ($Version -cnotmatch '^(0|[1-9][0-9]{0,4})\.(0|[1-9][0-9]{0,4})\.(0|[1-9][0-9]{0,4})(?:-preview\.([1-9][0-9]{0,4}))?$') {
    throw 'Use X.Y.Z or X.Y.Z-preview.N for -Version'
}
$versionParts = @([int]$Matches[1], [int]$Matches[2], [int]$Matches[3], [int]$Matches[4])
if ($versionParts | Where-Object { $_ -gt 65535 }) { throw 'Version components must fit in a Windows resource' }
$versionNumbers = $versionParts -join ','
$root = Split-Path -Parent $PSScriptRoot
if (-not $Clang) { $Clang = "$root/.tools/obs-native/llvm-mingw-20261117-ucrt-x86_64/bin/x86_64-w64-mingw32-clang.exe" }
if (-not $ObsSdk) { $ObsSdk = "$root/.tools/obs-sdk/obs-studio-30.1.2" }
if ($Go -eq 'go' -and (Test-Path -LiteralPath "$root/.tools/go/bin/go.exe")) { $Go = "$root/.tools/go/bin/go.exe" }
$Clang = (Resolve-Path -LiteralPath $Clang).Path
$ObsSdk = (Resolve-Path -LiteralPath $ObsSdk).Path
$ObsBin = (Resolve-Path -LiteralPath $ObsBin).Path
if (-not (Test-Path -LiteralPath "$ObsBin/obs.dll")) { throw 'obs.dll was not found in -ObsBin' }

# Stage the same wire, retry and WIC implementation as the standalone build.
$stage = "$root/build/obs-native"
New-Item -ItemType Directory -Path $stage -Force | Out-Null
foreach ($file in @('go.mod','go.sum','protocol.go','audio.go','audio_receive_windows.go','broadcast_windows.go','session.go','native_windows.go')) {
    Copy-Item -LiteralPath "$root/go-version/$file" -Destination $stage -Force
}
foreach ($file in @('plugin.go','bridge.h','module.c')) {
    Copy-Item -LiteralPath "$PSScriptRoot/$file" -Destination $stage -Force
}
$bundle = "$root/dist/obs-native"
$dllDir = "$bundle/obs-plugins/64bit"
New-Item -ItemType Directory -Path $dllDir -Force | Out-Null

# Stage the link input. The final linker runs outside the package directory.
Copy-Item -LiteralPath "$ObsBin/obs.dll" -Destination "$stage/obs.dll" -Force
# The file version is part of the DLL so a single file swap commits the version.
$resource = @"
1 VERSIONINFO
FILEVERSION $versionNumbers
PRODUCTVERSION $versionNumbers
FILEOS 0x40004L
FILETYPE 0x2L
BEGIN
    BLOCK "StringFileInfo"
    BEGIN
        BLOCK "040904b0"
        BEGIN
            VALUE "FileDescription", "WebcamGo Direct OBS plugin\0"
            VALUE "ProductName", "WebcamGo Direct\0"
            VALUE "FileVersion", "$Version\0"
            VALUE "ProductVersion", "$Version\0"
        END
    END
    BLOCK "VarFileInfo"
    BEGIN
        VALUE "Translation", 0x409, 1200
    END
END
"@
Set-Content -LiteralPath "$stage/version.rc" -Value $resource -Encoding ASCII
$windres = Join-Path (Split-Path -Parent $Clang) 'x86_64-w64-mingw32-windres.exe'
& $windres -O coff -i "$stage/version.rc" -o "$stage/version.syso"
if ($LASTEXITCODE -ne 0) { throw 'OBS plugin version resource failed' }
$env:CGO_ENABLED = '1'
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
$env:CC = $Clang
$env:CGO_CFLAGS = '-I "' + ($ObsSdk.Replace('\', '/') + '/libobs') + '"'
$env:CGO_LDFLAGS = '-L "' + ($stage.Replace('\', '/')) + '" -l:obs.dll'
Push-Location -LiteralPath $stage
try {
    & $Go build -trimpath -buildmode=c-shared -ldflags '-s -w' -o "$dllDir/webcamgo-direct.dll" .
    if ($LASTEXITCODE -ne 0) { throw 'OBS plugin build failed' }
} finally { Pop-Location }

Copy-Item -LiteralPath "$PSScriptRoot/README.md" -Destination "$bundle/README.md" -Force
New-Item -ItemType Directory -Path "$root/release" -Force | Out-Null
$archive = "$root/release/WebcamGo-OBS-Direct-Windows-x64.zip"
$package = "$root/build/obs-native-package"
New-Item -ItemType Directory -Path "$package/obs-plugins/64bit","$package/licenses" -Force | Out-Null
Copy-Item -LiteralPath "$dllDir/webcamgo-direct.dll" -Destination "$package/obs-plugins/64bit/webcamgo-direct.dll" -Force
Copy-Item -LiteralPath "$bundle/README.md" -Destination "$package/README.md" -Force
$goRoot = & $Go env GOROOT
$oleDir = & $Go -C "$stage" list -m -f '{{.Dir}}' github.com/go-ole/go-ole
$sysDir = & $Go -C "$stage" list -m -f '{{.Dir}}' golang.org/x/sys
Copy-Item -LiteralPath "$goRoot/LICENSE" -Destination "$package/licenses/Go-LICENSE.txt" -Force
Copy-Item -LiteralPath "$oleDir/LICENSE" -Destination "$package/licenses/go-ole-LICENSE.txt" -Force
Copy-Item -LiteralPath "$sysDir/LICENSE" -Destination "$package/licenses/x-sys-LICENSE.txt" -Force
Copy-Item -LiteralPath "$sysDir/PATENTS" -Destination "$package/licenses/x-sys-PATENTS.txt" -Force
Copy-Item -LiteralPath "$PSScriptRoot/COPYING","$PSScriptRoot/LICENSING.md" -Destination "$package/licenses" -Force
$toolchainLicense = Join-Path (Split-Path -Parent (Split-Path -Parent $Clang)) 'LICENSE.TXT'
if (Test-Path -LiteralPath $toolchainLicense) {
    Copy-Item -LiteralPath $toolchainLicense -Destination "$package/licenses/LLVM-MinGW-LICENSE.txt" -Force
}
Compress-Archive -LiteralPath "$package/obs-plugins","$package/README.md","$package/licenses" -DestinationPath $archive -Force
if ($PluginOnly -or -not (Test-Path -LiteralPath "$PSScriptRoot/setup/main_windows.go")) {
    Get-FileHash -LiteralPath $archive -Algorithm SHA256
    return
}

# A fixed two-file update contract lets the original updater install compatible
# future releases without extracting arbitrary paths into the OBS directory.
$out = "$root/release/obs-direct/v$Version"
$update = "$root/build/obs-native-update"
$setupStage = "$root/build/obs-native-setup"
New-Item -ItemType Directory -Path $out,$update,$setupStage -Force | Out-Null
$updateZip = "$out/WebcamGo-OBS-Direct-Update-Windows-x64.zip"
$setupExe = "$out/WebcamGo-OBS-Direct-Setup-Windows-x64.exe"
$dll = "$dllDir/webcamgo-direct.dll"
$manifest = @{
    format = 1
    product = 'WebcamGoDirect'
    runtime = 'windows-x64'
    version = $Version
    sha256 = (Get-FileHash -LiteralPath $dll -Algorithm SHA256).Hash.ToLowerInvariant()
} | ConvertTo-Json -Compress
[IO.File]::WriteAllText("$update/package-info.json", $manifest, [Text.UTF8Encoding]::new($false))
Compress-Archive -LiteralPath "$update/package-info.json",$dll -DestinationPath $updateZip -Force

Copy-Item -LiteralPath "$root/go-version/go.mod","$root/go-version/go.sum","$root/go-version/app.manifest" -Destination $setupStage -Force
Get-ChildItem -LiteralPath "$PSScriptRoot/setup" -Filter '*.go' -File | Copy-Item -Destination $setupStage -Force
Copy-Item -LiteralPath $updateZip -Destination "$setupStage/bundle.zip" -Force
@'
package main
import _ "embed"
//go:embed bundle.zip
var payload []byte
func init() { bundled = payload }
'@ | Set-Content -LiteralPath "$setupStage/payload.go" -Encoding ASCII
$env:CGO_ENABLED = '0'
Push-Location -LiteralPath $setupStage
try {
    & $Go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -manifest app.manifest -o setup.syso
    if ($LASTEXITCODE -ne 0) { throw 'Setup manifest generation failed' }
    & $Go test -count=1 .
    if ($LASTEXITCODE -ne 0) { throw 'Setup tests failed' }
    & $Go build -trimpath -ldflags '-H=windowsgui -s -w' -o $setupExe .
    if ($LASTEXITCODE -ne 0) { throw 'Setup build failed' }
} finally { Pop-Location }
$noticeDir = "$root/build/obs-native-notices"
New-Item -ItemType Directory -Path $noticeDir -Force | Out-Null
Copy-Item -Path "$package/licenses/*" -Destination $noticeDir -Force
$moduleLines = & $Go -C "$setupStage" list -deps -f '{{with .Module}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}' . | Sort-Object -Unique
if ($LASTEXITCODE -ne 0) { throw 'Setup dependency inventory failed' }
foreach ($line in $moduleLines) {
    $fields = $line.Split('|')
    if ($fields.Length -ne 3 -or -not $fields[1]) { continue }
    $moduleName = $fields[0].Replace('/','_')
    foreach ($notice in (Get-ChildItem -LiteralPath $fields[2] -File | Where-Object Name -Match '^(LICENSE|COPYING|COPYRIGHT|PATENTS)')) {
        Copy-Item -LiteralPath $notice.FullName -Destination "$noticeDir/$moduleName-$($notice.Name).txt" -Force
    }
}
$noticeZip = "$out/WebcamGo-OBS-Direct-Licenses.zip"
Compress-Archive -Path "$noticeDir/*" -DestinationPath $noticeZip -Force
Get-FileHash -LiteralPath $archive,$updateZip,$setupExe,$noticeZip -Algorithm SHA256
