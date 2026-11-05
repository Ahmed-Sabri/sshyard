#!/usr/bin/env python3
"""Exercise bump.sh in temporary repositories, including local-only pushes."""

from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parent.parent / "bump.sh"
CONFIG = '{\n  "name": "Release test",\n  "version": "1.2.3"\n}\n'


class BumpTest(unittest.TestCase):
    def setUp(self):
        work = tempfile.TemporaryDirectory()
        self.addCleanup(work.cleanup)
        self.root = Path(work.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.git("init", "-b", "main")
        for key, value in {
            "user.name": "Release test",
            "user.email": "release@example.invalid",
            "commit.gpgsign": "false",
            "tag.gpgsign": "false",
            "core.hooksPath": str(self.root / "no-hooks"),
        }.items():
            self.git("config", key, value)
        (self.repo / "mygo.json").write_text(CONFIG)
        for name in ("mixed.txt", "unstaged.txt", "deleted.txt"):
            (self.repo / name).write_text("initial\n")
        self.git("add", ".")
        self.git("commit", "-m", "initial")

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.repo, text=True).strip()

    def bump(self, version="1.2.4", push=False):
        command = ["bash", str(SCRIPT), "-v", version]
        if push:
            command.append("-p")
        return subprocess.run(command, cwd=self.repo, capture_output=True, text=True)

    def assert_release(self, parent):
        self.assertEqual(self.git("rev-parse", "HEAD^"), parent)
        self.assertEqual(self.git("show", "--format=", "--name-only", "HEAD"), "mygo.json")
        self.assertEqual(self.git("log", "-1", "--format=%s"), "chore(release): bump version to 1.2.4")
        self.assertEqual(self.git("rev-parse", "v1.2.4^{}"), self.git("rev-parse", "HEAD"))
        self.assertEqual((self.repo / "mygo.json").read_text(), CONFIG.replace("1.2.3", "1.2.4"))

    def test_clean_release(self):
        parent = self.git("rev-parse", "HEAD")
        result = self.bump()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assert_release(parent)
        self.assertEqual(self.git("status", "--porcelain"), "")

    def test_dirty_release_preserves_other_changes_and_pushes_only_release(self):
        remote = self.root / "origin.git"
        self.git("init", "--bare", "-b", "main", str(remote))
        self.git("remote", "add", "origin", str(remote))
        (self.repo / "mixed.txt").write_text("staged\n")
        (self.repo / "added.txt").write_text("staged new file\n")
        self.git("add", "mixed.txt", "added.txt")
        (self.repo / "mixed.txt").write_text("unstaged after staging\n")
        (self.repo / "unstaged.txt").write_text("unstaged\n")
        (self.repo / "untracked.txt").write_text("untracked\n")
        (self.repo / "deleted.txt").unlink()
        status = self.git("status", "--porcelain", "--untracked-files=all")
        staged = self.git("diff", "--cached", "--binary")
        unstaged = self.git("diff", "--binary")
        parent = self.git("rev-parse", "HEAD")
        result = self.bump(push=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assert_release(parent)
        self.assertEqual(self.git("status", "--porcelain", "--untracked-files=all"), status)
        self.assertEqual(self.git("diff", "--cached", "--binary"), staged)
        self.assertEqual(self.git("diff", "--binary"), unstaged)
        self.assertEqual((self.repo / "untracked.txt").read_text(), "untracked\n")
        for ref in ("refs/heads/main", "refs/tags/v1.2.4^{}"):
            self.assertEqual(self.git("--git-dir", str(remote), "rev-parse", ref), self.git("rev-parse", "HEAD"))

    def test_release_includes_existing_config_edits_only(self):
        config = self.repo / "mygo.json"
        config.write_text(CONFIG.replace("Release test", "staged config"))
        (self.repo / "mixed.txt").write_text("unrelated staged edit\n")
        self.git("add", "mygo.json", "mixed.txt")
        config.write_text(CONFIG.replace("Release test", "working config"))
        result = self.bump()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        expected = CONFIG.replace("Release test", "working config").replace("1.2.3", "1.2.4")
        self.assertEqual(config.read_text(), expected)
        self.assertEqual(self.git("show", "HEAD:mygo.json"), expected.strip())
        self.assertEqual(self.git("show", "--format=", "--name-only", "HEAD"), "mygo.json")
        self.assertEqual(self.git("diff", "--cached", "--name-only"), "mixed.txt")
        self.assertEqual(self.git("show", ":mixed.txt"), "unrelated staged edit")

    def test_invalid_version_leaves_changes_untouched(self):
        (self.repo / "mixed.txt").write_text("staged\n")
        self.git("add", "mixed.txt")
        parent = self.git("rev-parse", "HEAD")
        staged = self.git("diff", "--cached", "--binary")
        for version, error in (
            ("1.2.3", "already at"),
            ("1.2.2", "not above"),
            ("not-a-version", "must use x.y.z"),
        ):
            with self.subTest(version=version):
                result = self.bump(version)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(error, result.stderr)
                self.assertEqual(self.git("rev-parse", "HEAD"), parent)
                self.assertEqual((self.repo / "mygo.json").read_text(), CONFIG)
                self.assertEqual(self.git("diff", "--cached", "--binary"), staged)
                self.assertEqual(self.git("tag", "--list"), "")


if __name__ == "__main__":
    unittest.main()
