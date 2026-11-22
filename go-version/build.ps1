# Сборка и упаковка без Cgo; контракты артефактов: docs/modules/build.md.
param([string]$Go = 'go')
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $PSScriptRoot
if ($Go -eq 'go' -and (Test-Path -LiteralPath "$projectRoot/.tools/go/bin/go.exe")) {
    $Go = "$projectRoot/.tools/go/bin/go.exe"
}
# Только Go и системные DLL Windows; язык Go не требуется на целевом ПК.
$env:CGO_ENABLED = '0'
& $Go test -count=1 ./...
if ($LASTEXITCODE -ne 0) { throw 'Go tests failed' }
& $Go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -manifest app.manifest -o app.syso
if ($LASTEXITCODE -ne 0) { throw 'Manifest generation failed' }
$bundle = Join-Path $projectRoot 'dist/go-obs/WebcamGoOBS'
New-Item -ItemType Directory -Path $bundle -Force | Out-Null
& $Go build -trimpath -ldflags '-H=windowsgui -s -w' -o "$bundle/WebcamGo.exe" .
if ($LASTEXITCODE -ne 0) { throw 'Go build failed' }
Copy-Item -LiteralPath README.md -Destination "$bundle/README.md"
Copy-Item -LiteralPath OBS.md -Destination "$bundle/OBS.md"
Copy-Item -LiteralPath Setup-OBS.ps1, SETUP_OBS.bat -Destination $bundle
$obsCommand = '@echo off' + "`r`n" + 'start "" "%~dp0WebcamGo.exe" obs' + "`r`n"
[IO.File]::WriteAllText("$bundle/START_OBS.bat", $obsCommand, [Text.Encoding]::ASCII)
foreach ($role in @('CLIENT', 'SERVER', 'LOCAL')) {
    $command = "@echo off`r`ncd /d `"%~dp0`"`r`nstart `"`" `"%~dp0WebcamGo.exe`" $($role.ToLowerInvariant())`r`n"
    [IO.File]::WriteAllText("$bundle/START_$role.bat", $command, [Text.Encoding]::ASCII)
}
$licenses = Join-Path $bundle 'licenses'
New-Item -ItemType Directory -Path $licenses -Force | Out-Null
$moduleLines = & $Go list -m -f '{{.Path}}|{{.Version}}|{{.Dir}}' all
if ($LASTEXITCODE -ne 0) { throw 'Module inventory failed' }
$inventory = @('Third-party Go modules (source, version, license):')
foreach ($line in $moduleLines) {
    $parts = $line.Split('|')
    if (-not $parts[1]) { continue }
    $safeName = $parts[0].Replace('/', '_')
    $found = Get-ChildItem -LiteralPath $parts[2] -File | Where-Object { $_.Name -match '^(LICENSE|COPYING|COPYRIGHT)' }
    if (-not $found) { throw "License missing: $($parts[0])" }
    foreach ($file in $found) {
        Copy-Item -LiteralPath $file.FullName -Destination "$licenses/${safeName}_$($file.Name).txt" -Force
    }
    $inventory += "$($parts[0]) $($parts[1]) https://$($parts[0])"
}
$goRoot = & $Go env GOROOT
Copy-Item -LiteralPath "$goRoot/LICENSE" -Destination "$licenses/Go-LICENSE.txt" -Force
$inventory | Set-Content -LiteralPath "$licenses/THIRD-PARTY.txt" -Encoding UTF8
New-Item -ItemType Directory -Path "$projectRoot/release" -Force | Out-Null
$archive = Join-Path $projectRoot 'release/WebcamGo-OBS-Windows-x64.zip'
# Явный allowlist не допускает попадания профилей, логов и снимков в ZIP.
$packageFiles = @("$bundle/WebcamGo.exe", "$bundle/README.md", "$bundle/OBS.md", "$bundle/START_OBS.bat", "$bundle/SETUP_OBS.bat", "$bundle/Setup-OBS.ps1", "$bundle/START_CLIENT.bat", "$bundle/START_SERVER.bat", "$bundle/START_LOCAL.bat", $licenses)
Compress-Archive -LiteralPath $packageFiles -DestinationPath $archive -Force
Get-Item -LiteralPath "$bundle/WebcamGo.exe", $archive | Select-Object FullName, Length
$archiveHash = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash
Write-Host "SHA256: $archiveHash"
