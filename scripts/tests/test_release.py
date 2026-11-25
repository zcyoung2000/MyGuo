"""Exercise runner failure/recovery without live dispatches or real waiting."""
import base64
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPTS = Path(__file__).resolve().parents[1]


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, SCRIPTS / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


release = load("release_supervisor", "release.py")
verifier = load("ffmpeg_verifier", "verify-ffmpeg-minimal.py")


class FakeClock:
    def __init__(self):
        self.now = 0

    def __call__(self):
        return self.now

    def sleep(self, seconds):
        self.now += seconds


class FakeGitHub:
    def __init__(self, modes=("success",), *, dispatch_timeout=False):
        self.modes = modes
        self.dispatch_timeout = dispatch_timeout
        self.runs = {}
        self.events = []
        self.version = "0.1.7"
        self.cancel_works = True

    def __call__(self, path, method="GET", payload=None):
        if path.endswith("/dispatches"):
            run_id = len(self.runs) + 1
            inputs = payload["inputs"]
            self.events.append(("dispatch", inputs["macos_runner"]))
            self.runs[run_id] = dict(
                id=run_id, display_title=f"Release build v0.1.7 [{inputs['request_id']}]",
                html_url=f"https://example.test/runs/{run_id}", mode=self.modes[run_id - 1],
                status="in_progress", conclusion=None, runner=inputs["macos_runner"],
                publish=inputs["publish"],
            )
            if self.dispatch_timeout:
                raise release.ReleaseError("Dispatch response timed out after acceptance")
            return None
        if "/workflows/" in path and "/runs?" in path:
            return {"workflow_runs": list(self.runs.values())}
        if "/annotations?" in path:
            return [{"message": "The configuration 'macos-15-intel' is not supported"}]
        if "/runs/" in path:
            suffix = path.split("/runs/")[1]
            run = self.runs[int(suffix.split("/")[0])]
            if method == "POST":
                self.events.append((suffix.split("/")[1], run["id"]))
                if self.cancel_works:
                    run.update(status="completed", conclusion="cancelled")
                return None
            if suffix.endswith("/jobs?per_page=100"):
                mode = run["mode"]
                queued = mode in ("queue", "linux_queue", "unsupported")
                return {"jobs": [{
                    "id": run["id"], "name": "Verify source on macOS",
                    "check_run_url": f"repos/owner/repo/check-runs/{run['id']}",
                    "labels": ["ubuntu-24.04"] if mode == "linux_queue" else [run["runner"]],
                    "status": "completed" if mode in ("success", "code_failure", "unsupported") else "in_progress",
                    "conclusion": {"success": "success", "code_failure": "failure", "unsupported": "cancelled"}.get(mode),
                    "runner_id": 0 if queued else 123,
                    "started_at": "2026-11-25T00:00:00Z",  # not proof of acquisition
                    "completed_at": None,
                    "steps": [] if queued else [{"status": "in_progress"}],
                }]}
            if run["status"] != "completed":
                outcome = {"success": "success", "code_failure": "failure", "unsupported": "failure"}.get(run["mode"])
                if outcome:
                    run.update(status="completed", conclusion=outcome)
            return run
        if path.endswith("/mygo.json?ref=" + "a" * 40):
            return {"content": base64.b64encode(json.dumps({"version": self.version}).encode()).decode()}
        if "/commits/" in path:
            return {"sha": ("a" if path.endswith("v0.1.7") else "b") * 40}
        if "/git/ref/tags/" in path:
            return {"object": {"type": "tag", "sha": "c" * 40}}
        if path.endswith("/workflows/release-build.yml"):
            return {"state": "active"}
        if path == "repos/owner/repo":
            return {"default_branch": "main"}
        raise AssertionError(f"Unexpected API request: {method} {path}")


class SupervisorTests(unittest.TestCase):
    def supervise(self, api, **kwargs):
        clock = FakeClock()
        return release.Supervisor("owner/repo", "v0.1.7", api=api,
                                  clock=clock, sleep=clock.sleep,
                                  queue_seconds=15, total_seconds=180, **kwargs)

    def run_quietly(self, supervisor):
        with contextlib.redirect_stdout(io.StringIO()):
            supervisor.run()

    def test_success_defaults_to_build_only(self):
        api = FakeGitHub()
        self.run_quietly(self.supervise(api))
        self.assertEqual(api.events, [("dispatch", release.RUNNERS[0])])
        self.assertEqual(api.runs[1]["publish"], "false")

    def test_publish_requires_explicit_opt_in(self):
        api = FakeGitHub()
        self.run_quietly(self.supervise(api, publish=True))
        self.assertEqual(api.runs[1]["publish"], "true")

    def test_queue_timeout_cancels_before_single_fallback(self):
        api = FakeGitHub(("queue", "success"))
        self.run_quietly(self.supervise(api))
        self.assertEqual(api.events, [
            ("dispatch", release.RUNNERS[0]), ("cancel", 1),
            ("dispatch", release.RUNNERS[1]),
        ])

    def test_unsupported_configuration_uses_annotations(self):
        api = FakeGitHub(("unsupported", "success"))
        self.run_quietly(self.supervise(api))
        self.assertEqual(len(api.runs), 2)

    def test_two_unavailable_images_stop(self):
        api = FakeGitHub(("queue", "queue"))
        with self.assertRaisesRegex(release.ReleaseError, "Both approved"):
            self.run_quietly(self.supervise(api))
        self.assertEqual(len(api.runs), 2)
        self.assertTrue(all(run["status"] == "completed" for run in api.runs.values()))

    def test_code_failure_never_changes_runner(self):
        api = FakeGitHub(("code_failure",))
        with self.assertRaisesRegex(release.ReleaseError, "No automatic code/signing retry"):
            self.run_quietly(self.supervise(api))
        self.assertEqual(len(api.runs), 1)

    def test_linux_queue_does_not_trigger_macos_retry(self):
        api = FakeGitHub(("linux_queue",))
        with self.assertRaisesRegex(release.ReleaseError, "whole-service capacity"):
            self.run_quietly(self.supervise(api))
        self.assertEqual(len(api.runs), 1)
        self.assertEqual(api.runs[1]["status"], "completed")

    def test_total_budget_cancels_running_build(self):
        api = FakeGitHub(("running",))
        supervisor = self.supervise(api)
        supervisor.total_seconds = 15
        with self.assertRaisesRegex(release.ReleaseError, "total budget"):
            self.run_quietly(supervisor)
        self.assertIn(("cancel", 1), api.events)
        self.assertEqual(len(api.runs), 1)

    def test_cannot_cancel_means_no_replacement_dispatch(self):
        api = FakeGitHub(("queue",))
        api.cancel_works = False
        with self.assertRaisesRegex(release.ReleaseError, "refusing to launch a duplicate"):
            self.run_quietly(self.supervise(api))
        self.assertEqual(len(api.runs), 1)

    def test_lost_dispatch_response_is_not_resent(self):
        api = FakeGitHub(dispatch_timeout=True)
        self.run_quietly(self.supervise(api))
        self.assertEqual(len(api.runs), 1)

    def test_tag_version_mismatch_fails_before_dispatch(self):
        api = FakeGitHub()
        api.version = "0.1.8"
        with self.assertRaisesRegex(release.ReleaseError, "does not match"):
            self.run_quietly(self.supervise(api))
        self.assertFalse(api.runs)

    def test_invalid_tag_is_rejected_without_api(self):
        def forbidden(*args):
            self.fail("API must not be called")
        with self.assertRaises(release.ReleaseError):
            release.preflight(forbidden, "owner/repo", "main; echo bad")

    def test_cleanup_cancels_only_recorded_run(self):
        api = FakeGitHub(("running",))
        supervisor = self.supervise(api)
        with contextlib.redirect_stdout(io.StringIO()):
            supervisor.dispatch("main", "a" * 40, "b" * 40, release.RUNNERS[0])
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory) / "state.json"
            state.write_text(json.dumps({"repo": "owner/repo", "run_id": 1}))
            release.cancel_from_state(state, api=api)
        self.assertEqual(api.events[-1], ("cancel", 1))


class HelperCacheTests(unittest.TestCase):
    def make_helper(self, root, target="windows/amd64"):
        root.mkdir(parents=True, exist_ok=True)
        binary = "ffmpeg.exe" if target.startswith("windows/") else "ffmpeg"
        for name in (binary, "ffmpeg-LICENSE.txt", "ffmpeg-README.txt"):
            (root / name).write_bytes(b"fixture " + name.encode())
        (root / binary).chmod(0o755)
        info = {
            "Format": "myguo-ffmpeg-minimal-v1", "Version": "9.0.2", "Target": target,
            "SourceSHA256": "8c3850283eb25fa026482078a04051e0be17347b09ef81a0849bec15a96e002e",
            "License": "LGPL-2.1-or-later",
            "BuildRecipeSHA256": hashlib.sha256((SCRIPTS / "build-ffmpeg-minimal.sh").read_bytes()).hexdigest(),
            "Files": {name: hashlib.sha256((root / name).read_bytes()).hexdigest()
                      for name in (binary, "ffmpeg-LICENSE.txt", "ffmpeg-README.txt")},
        }
        if target.startswith("darwin/"):
            info["DeploymentTarget"] = "13.0"
        (root / "ffmpeg-build-info.json").write_text(json.dumps(info))

    def test_corrupt_helper_cannot_be_reused(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_helper(root)
            (root / "ffmpeg.exe").write_bytes(b"corrupt")
            with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                verifier.verify(root, "windows/amd64")

    def test_wrong_recipe_cannot_be_reused(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.make_helper(root)
            manifest = root / "ffmpeg-build-info.json"
            info = json.loads(manifest.read_text())
            info["BuildRecipeSHA256"] = "0" * 64
            manifest.write_text(json.dumps(info))
            with self.assertRaisesRegex(ValueError, "BuildRecipeSHA256"):
                verifier.verify(root, "windows/amd64")

    def test_verified_windows_cache_skips_compiler_and_network(self):
        with tempfile.TemporaryDirectory() as directory:
            project = Path(directory)
            self.make_helper(project / "tmp_ffmpeg_minimal/output")
            env = {**os.environ, "MYGUO_PROJECT_ROOT": str(project), "MYGUO_ZIG": "/does/not/exist"}
            env.pop("MYGUO_FFMPEG_BUILD_ROOT", None)
            result = subprocess.run(["bash", str(SCRIPTS / "build-ffmpeg-minimal.sh"), "windows/amd64"],
                                    env=env, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("Using verified minimal FFmpeg", result.stdout)


if __name__ == "__main__":
    unittest.main()
