#!/usr/bin/env python3
"""Compare complete candidate archives and source inventories from two builds."""
from pathlib import Path
import sys
from release_package import digest
first,second=map(Path,sys.argv[1:])
archives=lambda root: sorted(p for p in root.iterdir() if p.name.endswith(('.zip','.tar.gz')))
a,b=archives(first),archives(second)
if len(a)!=1 or len(b)!=1 or a[0].name!=b[0].name or digest(a[0])!=digest(b[0]):
    raise SystemExit('FAIL: archive bytes differ')
if (first/'SOURCE_SHA256SUMS.txt').read_bytes()!=(second/'SOURCE_SHA256SUMS.txt').read_bytes():
    raise SystemExit('FAIL: source inventory differs')
print('PASS: identical archives and source inventories:',digest(a[0]))
