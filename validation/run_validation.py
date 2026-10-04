#!/usr/bin/env python3
"""Run TTD, RTO, and HPA validation scripts and generate a statistics report."""

import re
import statistics
import subprocess
import sys
import time
from datetime import datetime
from pathlib import Path

SCRIPT_DIR = Path(__file__).parent

N_RUNS = 10
N_HPA_RUNS = 5   # HPA is slow (~60-180s each); fewer iterations

OPERATOR_NS = "orca-system"
DAPP_NAME = "dapp-sample"


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def kubectl(*args: str, check: bool = False) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["kubectl", *args],
        capture_output=True,
        text=True,
        check=check,
    )


def wait_for_helmrelease_ready(timeout: int = 300) -> bool:
    """Wait until the HelmRelease has Ready=True. Returns False on timeout."""
    created = kubectl("wait", "helmrelease", DAPP_NAME, "-n", OPERATOR_NS,
                       "--for=create", f"--timeout={timeout}s")
    if created.returncode != 0:
        return False
    ready = kubectl("wait", "helmrelease", DAPP_NAME, "-n", OPERATOR_NS,
                     "--for=condition=Ready", f"--timeout={timeout}s")
    return ready.returncode == 0


def wait_for_deployment_scaled_down(namespace: str, deploy: str, timeout: int = 120) -> bool:
    """Poll until deployment is back to 1 ready replica."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        proc = kubectl(
            "get", "deployment", deploy,
            "-n", namespace,
            "-o", "jsonpath={.status.readyReplicas}",
        )
        try:
            if int(proc.stdout.strip() or "0") == 1:
                return True
        except ValueError:
            pass
        time.sleep(5)
    return False


def run_script(path: Path) -> tuple[bool, str, str]:
    """Run a shell script. Returns (ok, stdout, stderr)."""
    try:
        proc = subprocess.run(
            [str(path)],
            capture_output=True,
            text=True,
            timeout=600,
            cwd=SCRIPT_DIR,
        )
        ok = proc.returncode == 0
        return ok, proc.stdout, proc.stderr
    except subprocess.TimeoutExpired:
        return False, "", "TIMEOUT after 600s"
    except Exception as exc:
        return False, "", str(exc)


def parse_ms(output: str, pattern: str) -> int | None:
    m = re.search(pattern, output)
    return int(m.group(1)) if m else None


def compute_stats(values: list[int]) -> dict:
    if not values:
        return {}
    return {
        "n":      len(values),
        "min":    min(values),
        "max":    max(values),
        "mean":   statistics.mean(values),
        "median": statistics.median(values),
        "stdev":  statistics.stdev(values) if len(values) > 1 else 0.0,
    }


# ---------------------------------------------------------------------------
# Per-metric runners
# ---------------------------------------------------------------------------

def run_ttd(n: int) -> list[int]:
    results: list[int] = []
    for i in range(1, n + 1):
        print(f"  [{i}/{n}] running ttd.sh...", end=" ", flush=True)
        ok, stdout, stderr = run_script(SCRIPT_DIR / "ttd.sh")
        if not ok:
            print(f"FAILED\n        {stderr.splitlines()[0] if stderr else 'unknown error'}")
            continue
        ms = parse_ms(stdout, r"TTD:\s+(\d+)ms")
        if ms is None:
            print("FAILED (could not parse timing)")
            continue
        results.append(ms)
        print(f"{ms}ms")
        if i < n:
            print("    waiting for HelmRelease to be Ready before next run...", end=" ", flush=True)
            ok = wait_for_helmrelease_ready()
            print("Ready" if ok else "TIMEOUT — continuing anyway")
    return results


def run_rto(n: int) -> list[int]:
    results: list[int] = []
    for i in range(1, n + 1):
        print(f"  [{i}/{n}] running rto.sh...", end=" ", flush=True)
        ok, stdout, stderr = run_script(SCRIPT_DIR / "rto.sh")
        if not ok:
            print(f"FAILED\n        {stderr.splitlines()[0] if stderr else 'unknown error'}")
            continue
        ms = parse_ms(stdout, r"RTO:\s+(\d+)ms")
        if ms is None:
            print("FAILED (could not parse timing)")
            continue
        results.append(ms)
        print(f"{ms}ms")
        # RTO already waited for HelmRelease to be Ready — small buffer is enough
        if i < n:
            time.sleep(5)
    return results


def run_hpa(n: int) -> list[int]:
    results: list[int] = []
    target_ns_proc = kubectl(
        "get", "dapp", DAPP_NAME, "-n", OPERATOR_NS,
        "-o", "jsonpath={.spec.namespace}",
    )
    target_ns = target_ns_proc.stdout.strip() or "dapp-sample-system"

    for i in range(1, n + 1):
        print(f"  [{i}/{n}] running hpa.sh...", end=" ", flush=True)
        ok, stdout, stderr = run_script(SCRIPT_DIR / "hpa.sh")
        if not ok:
            print(f"FAILED\n        {stderr.splitlines()[0] if stderr else 'unknown error'}")
            continue
        ms = parse_ms(stdout, r"HPA scale-out:\s+(\d+)ms")
        if ms is None:
            print("FAILED (could not parse timing)")
            continue
        results.append(ms)
        print(f"{ms}ms")
        if i < n:
            print(f"    waiting for deployment to scale back to 1...", end=" ", flush=True)
            ok = wait_for_deployment_scaled_down(target_ns, DAPP_NAME)
            print("done" if ok else "TIMEOUT — continuing anyway")
    return results


# ---------------------------------------------------------------------------
# Reporting
# ---------------------------------------------------------------------------

def format_stats_block(label: str, unit: str, values: list[int]) -> str:
    s = compute_stats(values)
    if not s:
        return f"{label}\n  No data collected.\n"
    lines = [
        label,
        f"  Runs:   {s['n']}",
        f"  Min:    {s['min']}{unit}",
        f"  Max:    {s['max']}{unit}",
        f"  Mean:   {s['mean']:.1f}{unit}",
        f"  Median: {s['median']:.1f}{unit}",
        f"  StdDev: {s['stdev']:.1f}{unit}",
        f"  Raw:    {values}",
    ]
    return "\n".join(lines)


def build_report(ttd: list[int], rto: list[int], hpa: list[int]) -> str:
    sep = "=" * 52
    now = datetime.now().isoformat(timespec="seconds")
    parts = [
        sep,
        "  orca Validation Report",
        f"  {now}",
        sep,
        "",
        format_stats_block("TTD — Time to Detect (ms)", "ms", ttd),
        "",
        format_stats_block("RTO — Recovery Time Objective (ms)", "ms", rto),
        "",
        format_stats_block("HPA — Scale-out Latency (ms)", "ms", hpa),
        "",
        sep,
    ]
    return "\n".join(parts)


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> None:
    print("=== orca Validation Suite ===")
    print(f"Started: {datetime.now().isoformat(timespec='seconds')}")
    print()

    print(f"--- TTD ({N_RUNS} runs) ---")
    ttd_results = run_ttd(N_RUNS)
    print()

    print(f"--- RTO ({N_RUNS} runs) ---")
    rto_results = run_rto(N_RUNS)
    print()

    print(f"--- HPA ({N_HPA_RUNS} runs) ---")
    hpa_results = run_hpa(N_HPA_RUNS)
    print()

    report = build_report(ttd_results, rto_results, hpa_results)
    print(report)

    ts = datetime.now().strftime("%Y%m%d_%H%M%S")
    report_path = SCRIPT_DIR / f"validation_report_{ts}.txt"
    report_path.write_text(report + "\n")
    print(f"\nReport saved to: {report_path}")

    if not ttd_results and not rto_results and not hpa_results:
        sys.exit(1)


if __name__ == "__main__":
    main()
