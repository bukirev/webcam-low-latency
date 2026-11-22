"""Check local Markdown links and source-module documentation coverage.

No network access, application import or device capture. Run from any directory:
python tools/check_docs.py
"""
import re
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
DOCUMENTS = [ROOT / 'README.md', ROOT / 'AGENTS.md', ROOT / 'go-version/README.md',
             *sorted((ROOT / 'docs').rglob('*.md'))]


def check():
    errors = []
    linked = set()
    count = 0
    for document in DOCUMENTS:
        for raw in re.findall(r'\[[^\]]*\]\(([^)]+)\)', document.read_text(encoding='utf-8')):
            target = raw.strip().strip('<>')
            url = urlsplit(target)
            if url.scheme or url.netloc or not url.path:
                continue
            path = (document.parent / unquote(url.path)).resolve()
            count += 1
            linked.add(path)
            if not path.exists():
                errors.append(f'{document.relative_to(ROOT)}: missing {target}')
    sources = [*sorted((ROOT / 'go-version').glob('*.go')),
               *sorted(ROOT.glob('*.py')),
               *sorted((ROOT / 'tools').glob('*.py')),
               *sorted((ROOT / 'tests').glob('*.py'))]
    for path in sources:
        if path.resolve() not in linked:
            errors.append(f'Module has no documentation link: {path.relative_to(ROOT)}')
    if errors:
        raise SystemExit('\n'.join(errors))
    print(f'OK: {len(DOCUMENTS)} Markdown documents, {count} local links, '
          f'{len(sources)} source files covered')


if __name__ == '__main__':
    check()
