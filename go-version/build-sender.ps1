param(
    [string]$Version = '0.1.0-preview.2',
    [string]$Go = 'go',
    [string]$Windres
)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
if ($Version -cnotmatch '^(0|[1-9][0-9]{0,4})\.(0|[1-9][0-9]{0,4})\.(0|[1-9][0-9]{0,4})(?:-preview\.([1-9][0-9]{0,4}))?$') {
    throw 'Use X.Y.Z or X.Y.Z-preview.N for -Version'
}
$parts = @([int]$Matches[1], [int]$Matches[2], [int]$Matches[3], [int]$Matches[4])
if ($parts | Where-Object { $_ -gt 65535 }) { throw 'Version components must fit in a Windows resource' }
$numbers = $parts -join ','
if ($Go -eq 'go' -and (Test-Path -LiteralPath "$root/.tools/go/bin/go.exe")) { $Go = "$root/.tools/go/bin/go.exe" }
if (-not $Windres) {
    $Windres = "$root/.tools/obs-native/llvm-mingw-20261117-ucrt-x86_64/bin/x86_64-w64-mingw32-windres.exe"
}
$Windres = (Resolve-Path -LiteralPath $Windres).Path
$installerSource = "$root/obs-plugin/setup"
if (-not (Test-Path -LiteralPath "$installerSource/product_sender.go")) { throw 'Local installer sources are required' }
$env:GOOS = 'windows'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
$stage = "$root/build/sender-versioned"
$setup = "$root/build/sender-setup"
$out = "$root/release/sender/v$Version"
New-Item -ItemType Directory -Path $stage,$setup,$out -Force | Out-Null
New-Item -ItemType Directory -Path "$stage/internal","$setup/internal" -Force | Out-Null
Copy-Item -LiteralPath "$PSScriptRoot/internal/brand" -Destination "$stage/internal" -Recurse -Force
Copy-Item -LiteralPath "$PSScriptRoot/internal/brand" -Destination "$setup/internal" -Recurse -Force
Get-ChildItem -LiteralPath $PSScriptRoot -Filter '*.go' -File | Copy-Item -Destination $stage -Force
Copy-Item -LiteralPath "$PSScriptRoot/go.mod","$PSScriptRoot/go.sum","$PSScriptRoot/settings_schema.json" -Destination $stage -Force
$manifestPath = "$PSScriptRoot/app.manifest".Replace('\','/')
$icon = "$PSScriptRoot/internal/brand/assets/VB2Link.ico".Replace('\','/')
@"
1 24 "$manifestPath"
3 ICON "$icon"
1 VERSIONINFO
FILEVERSION $numbers
PRODUCTVERSION $numbers
FILEOS 0x40004L
FILETYPE 0x1L
BEGIN
    BLOCK "StringFileInfo"
    BEGIN
        BLOCK "040904b0"
        BEGIN
            VALUE "FileDescription", "VB2Link Sender\0"
            VALUE "ProductName", "VB2Link\0"
            VALUE "FileVersion", "$Version\0"
            VALUE "ProductVersion", "$Version\0"
        END
    END
    BLOCK "VarFileInfo"
    BEGIN
        VALUE "Translation", 0x409, 1200
    END
END
"@ | Set-Content -LiteralPath "$stage/version.rc" -Encoding ASCII
& $Windres -O coff -i "$stage/version.rc" -o "$stage/version.syso"
if ($LASTEXITCODE -ne 0) { throw 'Sender version resource failed' }

$exe = "$stage/WebcamGo.exe"
& $Go -C "$stage" test -count=1 .
if ($LASTEXITCODE -ne 0) { throw 'Sender tests failed' }
& $Go -C "$stage" build -trimpath -ldflags "-H=windowsgui -s -w -X main.version=$Version" -o $exe .
if ($LASTEXITCODE -ne 0) { throw 'Sender build failed' }
$manifest = @{
    format = 1
    product = 'WebcamGoSender'
    runtime = 'windows-x64'
    version = $Version
    sha256 = (Get-FileHash -LiteralPath $exe -Algorithm SHA256).Hash.ToLowerInvariant()
} | ConvertTo-Json -Compress
[IO.File]::WriteAllText("$stage/package-info.json", $manifest, [Text.UTF8Encoding]::new($false))
$archive = "$out/WebcamGo-Sender-Update-Windows-x64.zip"
Compress-Archive -LiteralPath "$stage/package-info.json",$exe -DestinationPath $archive -Force

Get-ChildItem -LiteralPath $installerSource -Filter '*.go' -File | Copy-Item -Destination $setup -Force
Copy-Item -LiteralPath "$PSScriptRoot/go.mod","$PSScriptRoot/go.sum","$PSScriptRoot/app.manifest" -Destination $setup -Force
Copy-Item -LiteralPath $archive -Destination "$setup/bundle.zip" -Force
@'
package main
import _ "embed"
//go:embed bundle.zip
var payload []byte
func init() { bundled = payload }
'@ | Set-Content -LiteralPath "$setup/payload.go" -Encoding ASCII
$setupExe = "$out/VB2Link-Sender-Setup-Windows-x64.exe"
& $Go -C "$setup" run github.com/akavel/rsrc@v0.10.2 -arch amd64 -manifest app.manifest -ico 'internal/brand/assets/VB2Link.ico' -o setup.syso
if ($LASTEXITCODE -ne 0) { throw 'Sender setup manifest failed' }
& $Go -C "$setup" test -tags sender -count=1 .
if ($LASTEXITCODE -ne 0) { throw 'Sender setup tests failed' }
& $Go -C "$setup" build -tags sender -trimpath -ldflags '-H=windowsgui -s -w' -o $setupExe .
if ($LASTEXITCODE -ne 0) { throw 'Sender setup build failed' }

$notices = "$root/build/sender-notices"
New-Item -ItemType Directory -Path $notices -Force | Out-Null
$goRoot = & $Go env GOROOT
Copy-Item -LiteralPath "$goRoot/LICENSE" -Destination "$notices/Go-LICENSE.txt" -Force
$dependencyFormat = '{{with .Module}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}'
$senderDependencies = & $Go -C "$stage" list -deps -f $dependencyFormat .
if ($LASTEXITCODE -ne 0) { throw 'Sender dependency inventory failed' }
$setupDependencies = & $Go -C "$setup" list -deps -tags sender -f $dependencyFormat .
if ($LASTEXITCODE -ne 0) { throw 'Sender setup dependency inventory failed' }
foreach ($entry in (($senderDependencies + $setupDependencies) | Sort-Object -Unique)) {
    $fields = $entry.Split('|')
    if ($fields.Length -ne 3 -or -not $fields[1]) { continue }
    $module = $fields[0].Replace('/','_')
    foreach ($notice in (Get-ChildItem -LiteralPath $fields[2] -File | Where-Object Name -Match '^(LICENSE|COPYING|COPYRIGHT|PATENTS)')) {
        Copy-Item -LiteralPath $notice.FullName -Destination "$notices/$module-$($notice.Name).txt" -Force
    }
}
Copy-Item -LiteralPath "$root/obs-plugin/COPYING","$root/obs-plugin/LICENSING.md" -Destination $notices -Force
$noticeZip = "$out/WebcamGo-Sender-Licenses.zip"
Compress-Archive -Path "$notices/*" -DestinationPath $noticeZip -Force
Get-FileHash -LiteralPath $archive,$setupExe,$noticeZip -Algorithm SHA256
