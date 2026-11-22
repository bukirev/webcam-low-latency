param([string]$Version = '4.3.0')
$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot

python -m PyInstaller --noconfirm --clean --onedir --windowed --name WebcamLowLatency --icon 'go-version/internal/brand/assets/VB2Link.ico' --distpath "dist/$Version" --workpath "build/$Version" --specpath build --collect-all pyaudiowpatch --collect-submodules mss camera.py
if ($LASTEXITCODE -ne 0) { throw 'PyInstaller failed' }

$bundlePath = Join-Path $PSScriptRoot "dist/$Version/WebcamLowLatency"
Copy-Item -Path 'START*.bat' -Destination $bundlePath
Copy-Item -LiteralPath README.md,README.txt,requirements.txt -Destination $bundlePath
python tools/package_release.py $bundlePath
if ($LASTEXITCODE -ne 0) { throw 'Packaging failed' }
