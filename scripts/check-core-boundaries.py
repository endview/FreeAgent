#!/usr/bin/env python3
"""Check production dependency direction without launching optional programs."""
import argparse
from pathlib import Path
import subprocess


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--go', default='go')
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    forbidden = (
        'github.com/endview/freeagent/internal/channels',
        'github.com/endview/freeagent/internal/channelplugin',
        'github.com/endview/affectweave',
    )
    for target in ('./cmd/freeagent', './internal/harness'):
        dependencies = subprocess.check_output([args.go, 'list', '-deps', target], cwd=root, text=True).splitlines()
        leaked = [name for name in dependencies if any(name == prefix or name.startswith(prefix + '/') for prefix in forbidden)]
        if leaked:
            raise SystemExit(target + ' imports optional implementation: ' + ', '.join(leaked))
        print(target + ': no optional platform/business implementation dependency')


if __name__ == '__main__':
    main()
