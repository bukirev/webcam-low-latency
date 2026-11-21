"""Collect dependency licenses and create a portable ZIP without user settings."""
from importlib.metadata import distribution
from pathlib import Path
import shutil
import sys
from zipfile import ZipFile, ZIP_DEFLATED

bundle = Path(sys.argv[1]).resolve()
licenses = bundle / 'third-party-licenses'
for name in ('opencv-python', 'numpy', 'Pillow', 'mss', 'PyAudioWPatch'):
    dist = distribution(name)
    for file in dist.files or ():
        if any(token in file.name.lower() for token in ('license', 'copying', 'notice')):
            source = Path(dist.locate_file(file))
            if source.is_file():
                target = licenses / name / file
                # Metadata paths from installed wheels must remain in this bundle.
                if not target.resolve().is_relative_to(licenses.resolve()):
                    raise ValueError('Unexpected dependency license path: ' + str(file))
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(source, target)
release = Path('release')
release.mkdir(exist_ok=True)
target = release / 'WebcamLowLatency-Windows-x64.zip'
with ZipFile(target, 'w', compression=ZIP_DEFLATED, compresslevel=6) as archive:
    for file in sorted(bundle.rglob('*')):
        if file.is_file() and not file.name.startswith('settings_') and file.suffix != '.log':
            archive.write(file, Path(bundle.name) / file.relative_to(bundle))
with ZipFile(target) as archive:
    if archive.testzip() is not None:
        raise ValueError('ZIP verification failed')
    names = set(archive.namelist())
    for required in ('WebcamLowLatency.exe', 'START_SCREEN_CLIENT.bat', 'START_SCREEN_SERVER.bat',
                     'START_CLIENT.bat', '_internal/python313.dll'):
        if bundle.name + '/' + required not in names:
            raise ValueError('Missing bundle file: ' + required)
print(f'Portable ZIP: {target.resolve()} ({target.stat().st_size:,} bytes)')
