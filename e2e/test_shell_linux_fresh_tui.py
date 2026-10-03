"""Harness contract tests with fake supervisors; NOT Gentle Shell E2E evidence."""
import argparse
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

import shell_linux_fresh_tui as driver

FAKE = """#!/usr/bin/env python3
import json, os, pathlib, signal, sys, tty
tty.setcbreak(0)
print('Gentle Shell Linux user installer', flush=True)
target = pathlib.Path(sys.stdin.readline().strip())
print('Confirm this physical selection', flush=True)
assert sys.stdin.read(1) == 'y'
target.mkdir()
FAULT
(target / 'bin').mkdir()
launcher = '#!/usr/bin/env python3\\nimport signal,sys\\nsignal.signal(signal.SIGINT,lambda *args: sys.exit(0))\\nprint("launcher ready",flush=True)\\nfor line in sys.stdin:\\n if line.strip()=="/gentle:status": print("el Gentleman package is active.",flush=True)\\n'
for name in ('gentle-shell', 'pi'):
 p = target / 'bin' / name
 p.write_text(launcher)
 p.chmod(0o700)
(target / 'installation.json').write_text(json.dumps({'Mode':'separate','Destination':str(target)}))
"""


class HarnessTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.project, self.fixture = self.root / 'project', self.root / 'pi-fixture'
        self.project.mkdir(mode=0o700)
        self.fixture.mkdir(mode=0o700)
        (self.fixture / 'pi').write_bytes(b'existing guest Pi bytes')
        (self.fixture / 'link').symlink_to('/not-followed')
        self.supervisor = self.root / 'supervisor'
        self.fake()
        self.args = argparse.Namespace(execute_in_guest=True, source_sha='a'*40,
            supervisor=str(self.supervisor), supervisor_sha256=driver.digest(self.supervisor),
            target=str(self.root/'fresh'), project=str(self.project), pi_fixture=str(self.fixture), timeout=3)

    def fake(self, fault=''):
        self.supervisor.write_text(FAKE.replace('FAULT', fault))
        self.supervisor.chmod(0o700)

    def run_case(self):
        self.args.supervisor_sha256 = driver.digest(self.supervisor)
        return driver.execute(self.args)

    def test_fake_install_launch_and_preservation(self):
        report = self.run_case()
        self.assertTrue(report['passed'], report)
        self.assertTrue(report['installation']['confirmed'])
        self.assertTrue(report['launch']['registered'])
        self.assertFalse(report['Ready'])

    def test_refuses_existing_and_dangling_targets(self):
        target = Path(self.args.target)
        for kind in ('directory', 'dangling'):
            with self.subTest(kind=kind):
                target.mkdir() if kind == 'directory' else target.symlink_to('/missing')
                self.assertFalse(self.run_case()['passed'])
                target.rmdir() if kind == 'directory' else target.unlink()

    def test_bad_hash_consent_and_overlap(self):
        for change in ({'supervisor_sha256':'0'*64}, {'execute_in_guest':False}, {'target':str(self.fixture/'child')}, {'source_sha':'invalid'}):
            with self.subTest(change=change), patch.dict(vars(self.args), change):
                with self.assertRaises(RuntimeError):
                    driver.validate(self.args)

    def test_fixture_drift_and_partial_failure_are_not_passes(self):
        self.fake(f"pathlib.Path({str(self.fixture/'pi')!r}).write_bytes(b'changed'); sys.exit(7)")
        report = self.run_case()
        self.assertFalse(report['passed'])
        self.assertFalse(report['piPreserved'])
        self.assertTrue(report['effectsUncertain'])
        self.assertTrue(Path(self.args.target).exists())

    def test_timeout_overflow_nonzero_reap(self):
        pid_file = self.root / 'pid'
        for body in ("import time; time.sleep(30)", "print('x'*10000,flush=True)", "sys.exit(7)"):
            with self.subTest(body=body):
                script = self.root / 'child.py'
                script.write_text(f"import os,sys\nopen({str(pid_file)!r},'w').write(str(os.getpid()))\n{body}\n")
                with self.assertRaises((RuntimeError, driver.subprocess.TimeoutExpired)):
                    driver.interact([sys.executable, str(script)], self.project, {'PATH':'/usr/bin:/bin'}, timeout=.2, limit=100)
                with self.assertRaises(ProcessLookupError):
                    os.kill(int(pid_file.read_text()), 0)


if __name__ == '__main__':
    unittest.main()
