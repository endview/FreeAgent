#!/usr/bin/env python3
"""Build Linux CLI/Web using the shared deterministic release builder."""
import argparse
from release_package import build
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--go', default='go')
parser.add_argument('--output-directory', required=True)
parser.add_argument('--allow-dirty', action='store_true')
parser.add_argument('--postgres-runtime')
args = parser.parse_args()
build('linux-amd64', args.go, args.output_directory, args.allow_dirty, args.postgres_runtime)
