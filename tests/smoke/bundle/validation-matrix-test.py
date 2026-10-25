"""Verify complete job coverage, case routing, failure propagation and Release gates."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[3]
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('bundle_matrix', ROOT / 'tests/smoke/bundle/validation-matrix.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class Validation(unittest.TestCase):
    def setUp(self):
        self.inventory = json.loads((ROOT / 'packaging/bundle/test-matrix.json').read_text())
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.scripts = self.root / 'tests/smoke/bundle'
        self.scripts.mkdir(parents=True)
        (self.root / 'packaging/bundle').mkdir(parents=True)
        (self.root / 'packaging/bundle/test-matrix.json').write_text(json.dumps(self.inventory))
        shutil.copy2(ROOT / 'versions.env', self.root / 'versions.env')
        for name in ('check.sh', 'validation-matrix.py'):
            shutil.copy2(ROOT / 'tests/smoke/bundle' / name, self.scripts / name)
        for name in ('container-smoke.sh', 'vm-smoke.sh', 'negative-runtime.sh', 'prepare-arch-arm.sh'):
            path = self.scripts / name
            path.write_text('#!/bin/bash\nprintf "%s\\n" "' + name + ':$*"\nexit "${' + name.split('.')[0].replace('-', '_').upper() + '_EXIT:-0}"\n')
            path.chmod(0o755)

    def run_case(self, arch, distribution, **environment):
        return subprocess.run([str(self.scripts / 'check.sh'), arch, distribution],
                              capture_output=True, text=True, env=dict(os.environ, **environment))

    def test_every_distribution_on_both_native_architectures(self):
        jobs = module.validation_matrix(self.inventory)['include']
        expected = {entry['id'] for entry in self.inventory['distributions']} | {'arch', 'scratch'}
        self.assertTrue({'debian', 'ubuntu', 'rocky', 'alpine', 'arch', 'scratch'} <= expected)
        self.assertEqual(len(jobs), 2 * len(expected))
        for arch, runner, machine in [('amd64', 'ubuntu-latest', 'x86_64'), ('arm64', 'ubuntu-24.04-arm', 'aarch64')]:
            selected = [job for job in jobs if job['arch'] == arch]
            self.assertEqual({job['distribution'] for job in selected}, expected)
            self.assertTrue(all(job['runner'] == runner and job['machine'] == machine for job in selected))

    def test_invalid_inventory_fails_before_expensive_work(self):
        for changed in ('duplicate', 'reserved', 'unsafe', 'unpinned', 'arch-unpinned', 'rootfs-unpinned', 'rootfs-http', 'empty'):
            inventory = copy.deepcopy(self.inventory)
            if changed == 'duplicate':
                inventory['distributions'][1]['id'] = inventory['distributions'][0]['id']
            elif changed == 'reserved':
                inventory['distributions'][0]['id'] = 'scratch'
            elif changed == 'unsafe':
                inventory['distributions'][0]['id'] = '../escape'
            elif changed == 'unpinned':
                inventory['distributions'][0]['image'] = 'debian:latest'
            elif changed == 'arch-unpinned':
                inventory['arch_amd64']['image'] = 'archlinux:latest'
            elif changed == 'rootfs-unpinned':
                inventory['arch_arm64']['sha256'] = 'missing'
            elif changed == 'rootfs-http':
                inventory['arch_arm64']['url'] = 'http://example.test/rootfs.tar.gz'
            else:
                inventory['distributions'] = []
            with self.subTest(changed=changed), self.assertRaises(ValueError):
                module.validation_matrix(inventory)

    def test_single_case_routes_to_pinned_container_and_actual_vm(self):
        result = self.run_case('amd64', 'rocky')
        self.assertEqual(result.returncode, 0, result.stderr)
        image = next(entry['image'] for entry in self.inventory['distributions'] if entry['id'] == 'rocky')
        self.assertIn(image + ' linux/amd64', result.stdout)
        self.assertIn('vm-smoke.sh:', result.stdout)
        self.assertIn('/opt/ovpn ' + str(self.root / 'temp/bundle-logs/amd64/rocky/vm'), result.stdout)
        self.assertNotIn('prepare-arch-arm.sh:', result.stdout)
        self.assertNotIn('negative-runtime.sh:', result.stdout)

    def test_scratch_and_arm_arch_have_distinct_required_suites(self):
        result = self.run_case('arm64', 'scratch')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('negative-runtime.sh:ovpn-bundle-test:scratch-linux-arm64', result.stdout)
        self.assertNotIn('vm-smoke.sh:', result.stdout)
        result = self.run_case('arm64', 'arch')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('prepare-arch-arm.sh:', result.stdout)
        self.assertIn('vm-smoke.sh:', result.stdout)

    def test_failures_are_not_hidden_by_log_capture(self):
        for distribution, environment, code, excluded in [
                ('debian', {'CONTAINER_SMOKE_EXIT': '42'}, 42, 'vm-smoke.sh:'),
                ('debian', {'VM_SMOKE_EXIT': '17'}, 17, 'negative-runtime.sh:'),
                ('scratch', {'NEGATIVE_RUNTIME_EXIT': '23'}, 23, 'vm-smoke.sh:')]:
            result = self.run_case('amd64', distribution, **environment)
            self.assertEqual(result.returncode, code, result.stderr)
            self.assertNotIn(excluded, result.stdout)
            self.assertTrue(any((self.root / 'temp/bundle-logs').rglob('*.log')))

    def test_unknown_case_and_architecture_refused_before_execution(self):
        for arch, distribution in [('amd64', 'unknown'), ('riscv64', 'debian'), ('amd64', '../escape')]:
            result = self.run_case(arch, distribution)
            self.assertNotEqual(result.returncode, 0)
            self.assertNotIn('container-smoke.sh:', result.stdout)

    def test_local_all_entrypoint_runs_every_suite(self):
        result = self.run_case('arm64', 'all')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.count('container-smoke.sh:'), 6)
        self.assertEqual(result.stdout.count('vm-smoke.sh:'), 5)
        self.assertEqual(result.stdout.count('negative-runtime.sh:'), 1)
        self.assertEqual(result.stdout.count('prepare-arch-arm.sh:'), 1)

    def test_release_requires_success_of_every_validation_job(self):
        workflow = (ROOT / '.github/workflows/bundle.yml').read_text()
        jobs = dict(re.findall(r'^  ([a-z-]+):\n(.*?)(?=^  [a-z-]+:|\Z)', workflow, re.MULTILINE | re.DOTALL))
        self.assertIn('go-version: 1.27.3', jobs['preflight'])
        self.assertIn('go vet ./...', jobs['preflight'])
        self.assertIn('go test -race ./...', jobs['preflight'])
        self.assertIn('needs: preflight', jobs['build'])
        self.assertIn('needs: [preflight, build]', jobs['validate'])
        self.assertIn('fromJSON(needs.preflight.outputs.matrix)', jobs['validate'])
        self.assertIn('needs: [build, validate]', jobs['attach'])
        self.assertNotIn('always()', jobs['attach'])
        self.assertNotIn('continue-on-error:', workflow)
        self.assertEqual(workflow.count('contents: write'), 1)
        self.assertIn('contents: write', jobs['attach'])
        self.assertIn('if: always()', jobs['validate'])
        self.assertIn('compression-level: 0', jobs['build'])


if __name__ == '__main__':
    unittest.main()
