param(
    [string]$Version = '0.1.0-preview.2',
    [string]$SenderVersion = '0.1.0-preview.2',
    [string]$ObsVersion = '0.1.0-preview.4',
    [string]$Go = 'go',
    [switch]$UseExisting
)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
foreach ($value in @($Version,$SenderVersion,$ObsVersion)) {
    if ($value -cnotmatch '^(0|[1-9][0-9]{0,4})\.(0|[1-9][0-9]{0,4})\.(0|[1-9][0-9]{0,4})(?:-preview\.([1-9][0-9]{0,4}))?$') {
        throw "Invalid release version: $value"
    }
}
if ($Go -eq 'go' -and (Test-Path -LiteralPath "$root/.tools/go/bin/go.exe")) { $Go = "$root/.tools/go/bin/go.exe" }
$source = "$root/obs-plugin/setup"
$policy = Get-Content -Raw -Encoding UTF8 -LiteralPath "$source/compatibility-policy.json" | ConvertFrom-Json
if (-not $policy.sender.PSObject.Properties[$SenderVersion] -or -not $policy.obs.PSObject.Properties[$ObsVersion]) {
    throw 'Add the immutable protocol contracts for both component versions before the release build'
}
if (-not $UseExisting) {
    powershell -NoProfile -ExecutionPolicy Bypass -File "$PSScriptRoot/build-sender.ps1" -Go $Go -Version $SenderVersion
    if ($LASTEXITCODE -ne 0) { throw 'Sender build failed' }
    powershell -NoProfile -ExecutionPolicy Bypass -File "$root/obs-plugin/build.ps1" -Go $Go -Version $ObsVersion
    if ($LASTEXITCODE -ne 0) { throw 'OBS plugin build failed' }
}

$sender = "$root/release/sender/v$SenderVersion"
$obs = "$root/release/obs-direct/v$ObsVersion"
$stage = "$root/build/webcamgo-unified-setup"
$out = "$root/release/suite/v$Version"
New-Item -ItemType Directory -Path $stage,$out -Force | Out-Null
New-Item -ItemType Directory -Path "$stage/internal" -Force | Out-Null
Copy-Item -LiteralPath "$PSScriptRoot/internal/brand" -Destination "$stage/internal" -Recurse -Force
$targets = @(
    @{ Key = 'sender'; Dir = $sender; Version = $SenderVersion; Archive = 'WebcamGo-Sender-Update-Windows-x64.zip'; License = 'WebcamGo-Sender-Licenses.zip' },
    @{ Key = 'obs'; Dir = $obs; Version = $ObsVersion; Archive = 'WebcamGo-OBS-Direct-Update-Windows-x64.zip'; License = 'WebcamGo-OBS-Direct-Licenses.zip' }
)
$catalog = @{ format = 1; release = $Version; components = @{} }
foreach ($target in $targets) {
    $entry = Get-Item -LiteralPath "$($target.Dir)/$($target.Archive)"
    $catalog.components[$target.Key] = @{
        package = @{
            version = $target.Version
            size = $entry.Length
            sha256 = (Get-FileHash -LiteralPath $entry.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
        }
        versions = $policy.($target.Key)
    }
    Copy-Item -LiteralPath $entry.FullName,"$($target.Dir)/$($target.License)" -Destination $out -Force
    Copy-Item -LiteralPath $entry.FullName -Destination "$stage/$($target.Key).zip" -Force
}
$manifest = "$out/WebcamGo-Compatibility.json"
[IO.File]::WriteAllText($manifest, ($catalog | ConvertTo-Json -Depth 10), [Text.UTF8Encoding]::new($false))
Copy-Item -LiteralPath $manifest -Destination "$stage/compatibility.json" -Force
Get-ChildItem -LiteralPath $source -Filter '*.go' -File | Copy-Item -Destination $stage -Force
Copy-Item -LiteralPath "$PSScriptRoot/go.mod","$PSScriptRoot/go.sum","$PSScriptRoot/app.manifest" -Destination $stage -Force
@'
package main
import _ "embed"
//go:embed compatibility.json
var offlineManifest []byte
//go:embed sender.zip
var offlineSender []byte
//go:embed obs.zip
var offlineOBS []byte
func init() {
    embeddedCatalog = offlineManifest
    embeddedPackages = [2][]byte{offlineSender, offlineOBS}
}
'@ | Set-Content -LiteralPath "$stage/bundles.go" -Encoding ASCII
$env:CGO_ENABLED = '0'
$env:GOARCH = 'amd64'
$env:GOOS = 'windows'
& $Go -C "$stage" run github.com/akavel/rsrc@v0.10.2 -arch amd64 -manifest app.manifest -ico 'internal/brand/assets/VB2Link.ico' -o setup.syso
if ($LASTEXITCODE -ne 0) { throw 'Setup resource build failed' }
& $Go -C "$stage" test -tags unified -count=1 .
if ($LASTEXITCODE -ne 0) { throw 'Compatibility or package checks failed' }
& $Go -C "$stage" build -tags unified -trimpath -ldflags '-H=windowsgui -s -w' -o "$out/VB2Link-Setup-Windows-x64.exe" .
if ($LASTEXITCODE -ne 0) { throw 'Unified installer build failed' }
Get-ChildItem -LiteralPath $out -File | Get-FileHash -Algorithm SHA256
