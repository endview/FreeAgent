"""Offline installer regressions; all filesystem changes stay in a temporary tree."""
import argparse
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import socket
import tempfile
import types
import unittest
from unittest.mock import patch, Mock

spec = importlib.util.spec_from_file_location('freeagent_installer', Path(__file__).with_name('install-server.py'))
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name).resolve()
        self.package = self.root / 'package'
        (self.package / 'bin').mkdir(parents=True)
        (self.package / 'FreeAgent').write_bytes(b'portable launcher\n')
        (self.package / 'bin/freeagent').write_bytes(b'fixture program\n')
        self.manifest = {'target': 'linux/amd64', 'source_dirty': False, 'candidate': 'v7.11.0-dev.1',
                         'source_base_commit': 'a' * 40, 'data_format': 'Harness-v8', 'files': []}
        for name in ('FreeAgent', 'bin/freeagent'):
            raw = (self.package / name).read_bytes()
            self.manifest['files'].append({'path': name, 'bytes': len(raw), 'sha256': hashlib.sha256(raw).hexdigest()})
        self.write_inventory()
        for name, relative in [('INSTALL_ROOT', 'opt'), ('MANAGER_ROOT', 'usr/local/bin'),
                               ('UNIT_ROOT', 'etc/systemd/system'), ('STATE_ROOT', 'var/lib'), ('RUNTIME_ROOT', 'run')]:
            path = self.root / relative
            path.mkdir(parents=True, exist_ok=True)
            setting = patch.object(installer, name, path)
            setting.start()
            self.addCleanup(setting.stop)
        (installer.RUNTIME_ROOT / 'systemd/system').mkdir(parents=True)
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            port = probe.getsockname()[1]
        self.args = argparse.Namespace(package=str(self.package), prefix=str(installer.INSTALL_ROOT / 'freeagent'),
                                       manager=str(installer.MANAGER_ROOT / 'freeagent'), user='freeagent', unit='freeagent',
                                       port=port, public_proxy_user=None, public_group='freeagent-public', check=True, start=False)
        self.mutations = Mock(side_effect=AssertionError('preflight attempted a host mutation'))
        patches = [patch.object(installer.sys, 'platform', 'linux'),
                   patch.object(installer.os, 'geteuid', return_value=0, create=True),
                   patch.object(installer.os, 'uname', return_value=types.SimpleNamespace(machine='x86_64'), create=True),
                   patch.object(installer.shutil, 'which', side_effect=lambda name: '/usr/bin/' + name),
                   patch.object(installer, 'pwd', types.SimpleNamespace(getpwnam=Mock(side_effect=KeyError('absent')))),
                   patch.object(installer.subprocess, 'run', return_value=types.SimpleNamespace(returncode=3)),
                   patch.object(installer, 'run', self.mutations)]
        for setting in patches:
            setting.start()
            self.addCleanup(setting.stop)

    def write_inventory(self):
        (self.package / 'manifest.json').write_text(json.dumps(self.manifest))
        names = [record['path'] for record in self.manifest['files']] + ['manifest.json']
        lines = [hashlib.sha256((self.package / name).read_bytes()).hexdigest() + '  ' + name for name in names]
        (self.package / 'SHA256SUMS.txt').write_text('\n'.join(lines) + '\n')

    def assert_unmodified(self):
        self.mutations.assert_not_called()
        for path in (Path(self.args.prefix), Path(self.args.manager), installer.STATE_ROOT / self.args.unit):
            self.assertFalse(path.exists(), str(path))

    def test_check_cli_is_read_only(self):
        argv = ['install-server.py', '--package', str(self.package), '--prefix', self.args.prefix,
                '--manager', self.args.manager, '--port', str(self.args.port), '--check']
        output = io.StringIO()
        with patch.object(installer.sys, 'argv', argv), contextlib.redirect_stdout(output):
            installer.main()
        report = json.loads(output.getvalue())
        self.assertTrue(report['ok'] and report['check_only'])
        self.assertFalse(report['changes_made'])
        self.assert_unmodified()

    def test_missing_optional_sandbox_does_not_block_host_installation(self):
        installer.shutil.which.side_effect = lambda name: None if name == 'bwrap' else '/usr/bin/' + name
        report = installer.preflight(self.args)
        self.assertEqual(len(report['warnings']), 1)
        self.assertIn('Full/host', report['warnings'][0])
        self.assert_unmodified()

    def test_missing_proxy_is_rejected_before_account_creation(self):
        self.args.public_proxy_user = 'missing-proxy'
        with self.assertRaisesRegex(SystemExit, 'proxy account does not exist'):
            installer.preflight(self.args)
        self.assert_unmodified()

    def test_occupied_port_has_action_without_changes(self):
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', self.args.port))
            with self.assertRaisesRegex(SystemExit, 'Next: Choose a free --port'):
                installer.preflight(self.args)
        self.assert_unmodified()

    @unittest.skipUnless(os.name == 'posix', 'Linux listener reuse semantics')
    def test_stopped_listener_time_wait_does_not_block_reinstallation(self):
        with socket.socket() as listener:
            listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            listener.bind(('127.0.0.1', self.args.port))
            listener.listen()
            with socket.create_connection(('127.0.0.1', self.args.port), timeout=2) as client:
                accepted, _ = listener.accept()
                accepted.shutdown(socket.SHUT_WR)
                self.assertEqual(client.recv(1), b'')
                accepted.close()
        installer.preflight(self.args)
        self.assert_unmodified()

    def test_active_owned_installation_has_upgrade_guidance(self):
        prefix, manager = Path(self.args.prefix), Path(self.args.manager)
        prefix.mkdir()
        unit = installer.UNIT_ROOT / 'freeagent.service'
        unit.write_text('owned unit')
        manager.write_text('owned manager')
        receipt = {'user': self.args.user, 'unit': self.args.unit, 'prefix': str(prefix), 'manager': str(manager),
                   'unit_text': 'owned unit', 'manager_text': 'owned manager'}
        (prefix / '.freeagent-install.json').write_text(json.dumps(receipt))
        installer.subprocess.run.return_value.returncode = 0
        with socket.socket() as listener:
            listener.bind(('127.0.0.1', self.args.port))
            with self.assertRaisesRegex(SystemExit, 'systemctl stop freeagent'):
                installer.preflight(self.args)
        self.mutations.assert_not_called()
        self.assertEqual(unit.read_text(), 'owned unit')
        self.assertEqual(manager.read_text(), 'owned manager')

    def test_unowned_state_is_rejected_before_account_creation(self):
        (installer.STATE_ROOT / 'freeagent').mkdir()
        with self.assertRaisesRegex(SystemExit, 'State path belongs to another user'):
            installer.preflight(self.args)
        self.mutations.assert_not_called()
        self.assertFalse(Path(self.args.prefix).exists())

    def test_parent_traversal_is_rejected(self):
        self.args.prefix = str(installer.INSTALL_ROOT / '..' / 'escape')
        with self.assertRaisesRegex(SystemExit, 'without .. components'):
            installer.preflight(self.args)
        self.mutations.assert_not_called()

    def test_parent_symlink_is_rejected(self):
        outside, link = self.root / 'outside', installer.INSTALL_ROOT / 'alias'
        outside.mkdir()
        try:
            link.symlink_to(outside, target_is_directory=True)
        except OSError:
            self.skipTest('OS does not allow creation of directory symlinks')
        self.args.prefix = str(link / 'freeagent')
        with self.assertRaisesRegex(SystemExit, 'contains a symbolic link'):
            installer.preflight(self.args)
        self.assertFalse((outside / 'freeagent').exists())
        self.mutations.assert_not_called()

    def test_payload_tampering_is_rejected(self):
        (self.package / 'bin/freeagent').write_bytes(b'tampered program')
        with self.assertRaisesRegex(SystemExit, 'Package (integrity|size)'):
            installer.preflight(self.args)
        self.assert_unmodified()

    def test_undeclared_payload_is_rejected(self):
        (self.package / 'extra').write_text('must not be installed')
        with self.assertRaisesRegex(SystemExit, 'additional files'):
            installer.preflight(self.args)
        self.assert_unmodified()

    def test_missing_executable_inventory_is_rejected(self):
        self.manifest['files'] = self.manifest['files'][:1]
        self.write_inventory()
        with self.assertRaisesRegex(SystemExit, 'required executables'):
            installer.preflight(self.args)
        self.assert_unmodified()

    def test_duplicate_inventory_is_rejected(self):
        self.manifest['files'].append(self.manifest['files'][0])
        self.write_inventory()
        with self.assertRaisesRegex(SystemExit, 'duplicate payload'):
            installer.preflight(self.args)
        self.assert_unmodified()

    def test_checksum_list_is_verified(self):
        path = self.package / 'SHA256SUMS.txt'
        path.write_text(path.read_text().replace('  FreeAgent', '  absent-file'))
        with self.assertRaisesRegex(SystemExit, 'checksum list differs'):
            installer.preflight(self.args)
        self.assert_unmodified()

    def test_used_portable_directory_is_rejected(self):
        (self.package / 'data').mkdir()
        with self.assertRaisesRegex(SystemExit, 'already contains user data'):
            installer.preflight(self.args)
        self.assert_unmodified()

    def test_failed_publication_retains_the_existing_file(self):
        path = self.root / 'managed-file'
        path.write_text('original')
        with patch.object(installer.os, 'replace', side_effect=OSError('publication failed')):
            with self.assertRaises(OSError):
                installer.atomic_text(path, 'new content', 0o600)
        self.assertEqual(path.read_text(), 'original')
        self.assertEqual(list(self.root.glob('.managed-file.*')), [])

    def test_publication_installs_complete_text_and_cleans_staging(self):
        path = self.root / 'managed-file'
        installer.atomic_text(path, 'complete\ncontent\n', 0o600)
        self.assertEqual(path.read_bytes(), b'complete\ncontent\n')
        if os.name == 'posix':
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(list(self.root.glob('.managed-file.*')), [])


if __name__ == '__main__':
    unittest.main()
