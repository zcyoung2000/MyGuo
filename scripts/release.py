#!/usr/bin/env python3
"""Supervise a tagged release with bounded queues and one runner fallback."""
import argparse
import base64
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time
from urllib.parse import quote
import uuid


RUNNERS = ("macos-15-intel", "macos-26-intel")
WORKFLOW = "release-build.yml"
INFRA_ERRORS = (
    "is not supported", "not acquired by runner", "capacity constraints",
    "failed to provision", "failed to acquire",
)


class ReleaseError(RuntimeError):
    pass


class RunnerUnavailable(ReleaseError):
    pass


def gh(path, method="GET", payload=None):
    command = ["gh", "api", "--method", method, path]
    if payload is not None:
        command += ["--input", "-"]
    try:
        result = subprocess.run(
            command, input=json.dumps(payload) if payload is not None else None,
            text=True, capture_output=True, timeout=30, check=True,
        )
    except subprocess.TimeoutExpired as error:
        raise ReleaseError(f"GitHub API timed out: {method} {path}") from error
    except subprocess.CalledProcessError as error:
        raise ReleaseError(error.stderr.strip() or f"GitHub API failed: {path}") from error
    return json.loads(result.stdout) if result.stdout.strip() else None


def preflight(api, repo, tag):
    if not re.fullmatch(r"v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?", tag):
        raise ReleaseError("Use an existing version tag such as v0.1.7.")
    if not re.fullmatch(r"[\w.-]+/[\w.-]+", repo):
        raise ReleaseError("Repository must be OWNER/REPO.")
    prefix = f"repos/{repo}"
    branch = api(prefix)["default_branch"]
    api(f"{prefix}/git/ref/tags/{quote(tag, safe='')}")
    source_sha = api(f"{prefix}/commits/{quote(tag, safe='')}")["sha"]
    content = api(f"{prefix}/contents/mygo.json?ref={source_sha}")
    config = json.loads(base64.b64decode(content["content"]))
    if tag != f"v{config['version']}":
        raise ReleaseError(f"Tag {tag} does not match mygo.json version {config['version']}.")
    tooling_sha = api(f"{prefix}/commits/{quote(branch, safe='')}")["sha"]
    workflow = api(f"{prefix}/actions/workflows/{WORKFLOW}")
    if workflow["state"] != "active":
        raise ReleaseError(f"Enable {WORKFLOW} on the default branch first.")
    return branch, source_sha, tooling_sha


class Supervisor:
    def __init__(self, repo, tag, *, publish=False, queue_seconds=180,
                 total_seconds=1800, state_file=None, api=gh,
                 clock=time.monotonic, sleep=time.sleep):
        self.repo, self.tag, self.publish = repo, tag, publish
        self.api, self.clock, self.sleep = api, clock, sleep
        self.queue_seconds, self.total_seconds = queue_seconds, total_seconds
        self.state_file = Path(state_file) if state_file else None
        self.started = clock()
        self.active_run = None
        self.cancellation_failed = False
        self.notes = []
        self.prefix = f"repos/{repo}/actions"

    def log(self, message):
        print(message, flush=True)
        self.notes.append(message)

    def check_budget(self):
        if self.clock() - self.started >= self.total_seconds:
            raise ReleaseError(f"Release exceeded the {self.total_seconds // 60}-minute total budget.")

    def write_state(self, **values):
        if self.state_file:
            self.state_file.write_text(json.dumps({"repo": self.repo, **values}) + "\n")

    def dispatch(self, branch, source_sha, tooling_sha, runner):
        request = uuid.uuid4().hex
        title = f"Release build {self.tag} [{request}]"
        # Persist correlation before dispatch: a timeout can still mean accepted.
        self.write_state(request_title=title)
        payload = {"ref": branch, "inputs": {
            "release_tag": self.tag, "source_sha": source_sha,
            "tooling_ref": tooling_sha, "macos_runner": runner,
            "request_id": request, "publish": "true" if self.publish else "false",
        }}
        dispatch_error = None
        try:
            self.api(f"{self.prefix}/workflows/{WORKFLOW}/dispatches", "POST", payload)
        except ReleaseError as error:
            # Never blindly resubmit an uncertain POST.
            dispatch_error = error
        deadline = self.clock() + 90
        while self.clock() < deadline:
            self.check_budget()
            runs = self.api(f"{self.prefix}/workflows/{WORKFLOW}/runs?event=workflow_dispatch&per_page=50")["workflow_runs"]
            matching = [run for run in runs if run["display_title"] == title]
            if matching:
                self.active_run = matching[0]["id"]
                self.write_state(run_id=self.active_run, request_title=title)
                self.log(f"Runner {runner}: {matching[0]['html_url']}")
                return self.active_run
            self.sleep(5)
        raise ReleaseError(f"No worker appeared within 90 seconds. {dispatch_error or 'Check Actions before dispatching again.'}")

    def jobs(self, run):
        return self.api(f"{self.prefix}/runs/{run}/jobs?per_page=100")["jobs"]

    def infrastructure_failure(self, jobs):
        failed = [job for job in jobs if job.get("conclusion") in ("failure", "cancelled", "timed_out", "startup_failure")]
        # A failure after acquisition must not repeat code/test/signing work.
        if not failed or any(job.get("runner_id") or job.get("steps") for job in failed):
            return False
        for job in failed:
            if not any(label.startswith("macos-") for label in job.get("labels", [])):
                return False
            annotations = self.api(job["check_run_url"] + "/annotations?per_page=100")
            text = " ".join(item.get("message", "") for item in annotations).lower()
            if not any(error in text for error in INFRA_ERRORS):
                return False
        return True

    def monitor(self, run):
        waiting = {}
        previous = None
        empty_since = self.clock()
        while True:
            self.check_budget()
            state = self.api(f"{self.prefix}/runs/{run}")
            jobs = self.jobs(run)
            status = "; ".join(f"{j['name']}: {j.get('conclusion') or j['status']}" for j in jobs)
            if status != previous:
                self.log(status or "Waiting for jobs to be scheduled")
                previous = status
            if state["status"] == "completed":
                if state["conclusion"] == "success":
                    for job in jobs:
                        self.log(f"Timing: {job['name']}: {job.get('started_at')} → {job.get('completed_at')}")
                    self.active_run = None
                    return
                if self.infrastructure_failure(jobs):
                    raise RunnerUnavailable("macOS runner configuration/capacity failure before any step ran.")
                raise ReleaseError(f"Worker ended with {state['conclusion']}; inspect {state['html_url']}. No automatic code/signing retry.")
            if not jobs and self.clock() - empty_since >= self.queue_seconds:
                raise ReleaseError("Worker jobs were not scheduled; check repository concurrency and GitHub Actions availability.")
            current_waiting = set()
            for job in jobs:
                if job["status"] == "completed":
                    continue
                # started_at can be set even when no runner was assigned.
                acquired = job.get("runner_id") or any(step["status"] != "queued" for step in job.get("steps", []))
                if acquired:
                    continue
                current_waiting.add(job["id"])
                waiting.setdefault(job["id"], self.clock())
                if self.clock() - waiting[job["id"]] >= self.queue_seconds:
                    message = f"{job['name']} had no runner for {self.queue_seconds}s."
                    if any(label.startswith("macos-") for label in job.get("labels", [])):
                        raise RunnerUnavailable(message)
                    raise ReleaseError(message + " Linux/whole-service capacity cannot be fixed by switching macOS images.")
            waiting = {key: value for key, value in waiting.items() if key in current_waiting}
            self.sleep(15)

    def cancel(self, run):
        if self.api(f"{self.prefix}/runs/{run}")["status"] == "completed":
            self.active_run = None
            return
        for endpoint, wait in (("cancel", 45), ("force-cancel", 30)):
            try:
                self.api(f"{self.prefix}/runs/{run}/{endpoint}", "POST")
            except ReleaseError:
                if self.api(f"{self.prefix}/runs/{run}")["status"] != "completed":
                    raise
            deadline = self.clock() + wait
            while self.clock() < deadline:
                if self.api(f"{self.prefix}/runs/{run}")["status"] == "completed":
                    self.log(f"Stopped worker {run} before any replacement dispatch.")
                    self.active_run = None
                    return
                self.sleep(5)
        self.cancellation_failed = True
        raise ReleaseError(f"Could not stop worker {run}; refusing to launch a duplicate release.")

    def run(self):
        branch, source, tooling = preflight(self.api, self.repo, self.tag)
        self.log(f"Preflight passed: {self.tag} → {source}; tooling {tooling} on {branch}")
        try:
            for attempt, runner in enumerate(RUNNERS):
                self.check_budget()
                run = self.dispatch(branch, source, tooling, runner)
                try:
                    self.monitor(run)
                    self.log(f"{'Published' if self.publish else 'Build-only validation passed'}: {self.tag}; {round(self.clock() - self.started)}s total.")
                    return
                except RunnerUnavailable as error:
                    self.log(str(error))
                    self.cancel(run)
                    if attempt == len(RUNNERS) - 1:
                        raise ReleaseError("Both approved Intel runner images were unavailable. Stop; use the documented local-build fallback.") from error
                    self.log(f"Switching once to {RUNNERS[attempt + 1]}.")
        finally:
            if self.active_run is not None and not self.cancellation_failed:
                self.cancel(self.active_run)


def cancel_from_state(path, api=gh):
    if not path.exists():
        return
    state = json.loads(path.read_text())
    supervisor = Supervisor(state["repo"], "", api=api)
    run = state.get("run_id")
    if run is None:
        runs = api(f"{supervisor.prefix}/workflows/{WORKFLOW}/runs?event=workflow_dispatch&per_page=50")["workflow_runs"]
        matches = [item for item in runs if item["display_title"] == state.get("request_title")]
        run = matches[0]["id"] if matches else None
    if run is not None:
        supervisor.cancel(run)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tag")
    parser.add_argument("--repo", default=os.environ.get("GITHUB_REPOSITORY", "zcyoung2000/MyGuo"))
    parser.add_argument("--publish", action="store_true", help="Publish only after all three platforms pass; default is build-only.")
    parser.add_argument("--preflight-only", action="store_true")
    parser.add_argument("--cancel-active", action="store_true")
    parser.add_argument("--state-file", type=Path)
    args = parser.parse_args()
    if args.cancel_active:
        if not args.state_file:
            parser.error("--cancel-active requires --state-file")
        cancel_from_state(args.state_file)
        return
    if not args.tag:
        parser.error("--tag is required")
    if args.preflight_only:
        print(json.dumps(dict(zip(("branch", "source_sha", "tooling_sha"), preflight(gh, args.repo, args.tag))), indent=2))
        return
    supervisor = Supervisor(args.repo, args.tag, publish=args.publish, state_file=args.state_file)
    try:
        supervisor.run()
    finally:
        if os.environ.get("GITHUB_STEP_SUMMARY"):
            with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as summary:
                summary.write("## Release supervision\n\n```text\n" + "\n".join(supervisor.notes) + "\n```\n")


if __name__ == "__main__":
    try:
        main()
    except (ReleaseError, OSError, ValueError, KeyError, KeyboardInterrupt) as error:
        print(f"Release stopped: {error}", file=sys.stderr)
        sys.exit(1)
