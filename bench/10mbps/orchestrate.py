"""Run both rate caps in an isolated Docker network; never change host qdiscs."""
import json
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent
RESULTS = ROOT / "results"
NETWORK = "gm-express-bench-isolated"
NAMES = {backend: "gm-express-bench-" + backend for backend in ("go", "js")}


def docker(*args, capture=True, check=True):
    return subprocess.run(["docker", *args], text=True, capture_output=capture, check=check)


def main():
    RESULTS.mkdir(exist_ok=True)
    # Never reuse or remove someone else's containers/network on a name collision.
    docker("network", "create", NETWORK)
    active = []
    try:
        images = {}
        for backend in ("go", "js", "client"):
            images[backend] = json.loads(docker("image", "inspect", "gm-express-bench-" + backend + ":local").stdout)
        (RESULTS / "images.json").write_text(json.dumps(images, indent=2))
        with tempfile.TemporaryDirectory(prefix="gm-express-bench-cache-") as cache:
            for rate in ("10mbit", "200mbit"):
                print("START_RATE", rate, flush=True)
                before = {}
                for backend, name in NAMES.items():
                    docker("run", "-d", "--name", name, "--network", NETWORK,
                           "--cap-add", "NET_ADMIN", "--cpus", "1", "--memory", "1g",
                           "-e", "BENCH_RATE=" + rate, "-e", "GM_EXPRESS_EXPIRATION=86400",
                           "gm-express-bench-" + backend + ":local")
                    active.append(name)
                # The first exec also checks startup/NET_ADMIN support before any timed work.
                for backend, name in NAMES.items():
                    before[backend] = json.loads(docker("exec", name, "tc", "-s", "-j", "qdisc", "show", "dev", "eth0").stdout)
                (RESULTS / f"{rate}-server-qdisc-before.json").write_text(json.dumps(before, indent=2))
                client = "gm-express-bench-client"
                docker("create", "--name", client, "--network", NETWORK,
                       "--cap-add", "NET_ADMIN", "--cpus", "2", "--memory", "1g",
                       "-e", "BENCH_RATE=" + rate,
                       "-v", str(ROOT) + ":/bench:ro", "-v", str(RESULTS) + ":/results",
                       "-v", cache + ":/cache", "gm-express-bench-client:local")
                active.append(client)
                docker("start", "-a", client, capture=False)
                exit_code = int(docker("inspect", "--format", "{{.State.ExitCode}}", client).stdout)
                if exit_code:
                    raise RuntimeError(f"benchmark client exited {exit_code}")
                after = {}
                for backend, name in NAMES.items():
                    after[backend] = json.loads(docker("exec", name, "tc", "-s", "-j", "qdisc", "show", "dev", "eth0").stdout)
                (RESULTS / f"{rate}-server-qdisc-after.json").write_text(json.dumps(after, indent=2))
                (RESULTS / f"{rate}-container-stats.txt").write_text(docker("stats", "--no-stream", "--format", "{{json .}}", *NAMES.values()).stdout)
                for name in list(reversed(active)):
                    docker("rm", "-f", name)
                    active.remove(name)
                print("END_RATE", rate, flush=True)
    finally:
        for name in reversed(active):
            diagnostics = docker("logs", "--tail", "50", name, check=False)
            print("CONTAINER_DIAGNOSTICS", name, diagnostics.stdout, diagnostics.stderr, flush=True)
            docker("rm", "-f", name, check=False)
        docker("network", "rm", NETWORK, check=False)


if __name__ == "__main__":
    main()
