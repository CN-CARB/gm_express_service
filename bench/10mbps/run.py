"""Real HTTP services + modeled addon cache flows, not an in-engine GMod benchmark."""
import concurrent.futures
import hashlib
import http.client
import json
import os
from pathlib import Path
import platform
import statistics
import subprocess
import tempfile
import time

SIZES = [("100KiB", 100 * 1024), ("1MiB", 1024 * 1024), ("10MiB", 10 * 1024 * 1024)]
RATE = os.environ.get("BENCH_RATE", "10mbit")
REPEATS = int(os.environ.get("BENCH_REPEATS", "3"))
CONCURRENT_ROUNDS = int(os.environ.get("BENCH_CONCURRENT_ROUNDS", "2"))
EXPRESS_DELAY = float(os.environ.get("EXPRESS_DELAY", "0.15"))
OUTPUT = Path("/results")
OUTPUT.mkdir(exist_ok=True)
ROWS = []


def call(connection, method, path, body=None):
    headers = {"Content-Type": "application/octet-stream"} if body is not None else {}
    start = time.perf_counter()
    connection.request(method, "/v1" + path, body=body, headers=headers)
    response = connection.getresponse()
    result = response.read()
    elapsed = time.perf_counter() - start
    if response.status != 200:
        raise RuntimeError(f"{method} endpoint failed: HTTP {response.status}, body length={len(result)}")
    return result, elapsed


class Flow:
    def __init__(self, backend, directory, index):
        self.backend = backend
        self.connection = http.client.HTTPConnection(
            "gm-express-bench-" + backend, 3000 if backend == "go" else 8000, timeout=180
        )
        raw, _ = call(self.connection, "GET", "/register")
        self.tokens = json.loads(raw)
        assert self.tokens["expiration"] == 86400
        self.cache = directory / f"{backend}-{index}.dat"
        self.file_id = None

    def close(self):
        self.connection.close()

    def send(self, payload, scenario, barrier=None):
        if barrier:
            barrier.wait()
        start = time.perf_counter()
        algorithm = hashlib.sha256 if self.backend == "go" else hashlib.sha1
        expected_hash = algorithm(payload).digest()
        sender_hash_s = time.perf_counter() - start
        upload_s = download_s = cache_work_s = 0.0
        upload_bytes = download_bytes = requests = 0
        if scenario == "cold":
            raw, upload_s = call(self.connection, "POST", "/write/" + self.tokens["server"], payload)
            self.file_id = json.loads(raw)["id"]
            upload_bytes = len(payload)
            requests += 1
        assert self.file_id is not None
        # Express defaults to a 150 ms notification delay. Expose HTTP-only
        # timing separately so this deliberate wait is never attributed to JS.
        delay = EXPRESS_DELAY if self.backend == "js" else 0.0
        if delay:
            time.sleep(delay)
        if self.backend == "go" and scenario == "receiver_cached":
            cache_start = time.perf_counter()
            body = self.cache.read_bytes()
            assert algorithm(body).digest() == expected_hash
            cache_work_s = time.perf_counter() - cache_start
        else:
            # Both addons skip /size when the optional download filter is absent.
            body, download_s = call(self.connection, "GET", "/read/" + self.tokens["client"] + "/" + self.file_id)
            download_bytes = len(body)
            requests += 1
            cache_start = time.perf_counter()
            assert algorithm(body).digest() == expected_hash
            if self.backend == "go":
                # Like file.Write: buffered write/close, not fsync. Warm reads may
                # hit the OS page cache; no disk cache flush is attempted.
                self.cache.write_bytes(body)
            cache_work_s = time.perf_counter() - cache_start
        total = time.perf_counter() - start
        assert len(body) == len(payload) and body == payload
        return {
            "backend": self.backend, "rate": RATE, "scenario": scenario,
            "size_bytes": len(payload), "total_s": total,
            "http_s": upload_s + download_s, "upload_s": upload_s,
            "download_s": download_s, "sender_hash_s": sender_hash_s,
            "receiver_work_s": cache_work_s, "notify_delay_s": delay,
            "http_requests": requests, "upload_bytes": upload_bytes,
            "download_bytes": download_bytes, "verified": True,
        }


def record(row):
    ROWS.append(row)
    with (OUTPUT / f"{RATE}-samples.jsonl").open("a") as stream:
        stream.write(json.dumps(row) + "\n")
    print(json.dumps(row), flush=True)


def main():
    import threading
    metadata = {
        "rate": RATE, "python": platform.python_version(), "sizes": SIZES,
        "sequential_repeats": REPEATS, "concurrent_rounds": CONCURRENT_ROUNDS,
        "express_delay_s": EXPRESS_DELAY,
        "scope": "Actual HTTP services; Python model of addon upload/receive caches; no GMod/net/Protobuf/serialization/TLS",
        "client_qdisc_before": json.loads(subprocess.check_output(["tc", "-s", "-j", "qdisc", "show", "dev", "eth0"])),
    }
    (OUTPUT / f"{RATE}-samples.jsonl").write_text("")
    with tempfile.TemporaryDirectory(prefix="gm-express-cache-", dir="/cache") as directory:
        directory = Path(directory)
        # Warm startup and token registration are deliberately outside measured transfers.
        for backend in ("go", "js"):
            flow = Flow(backend, directory, "warmup")
            flow.send(os.urandom(1024), "cold")
            flow.close()
        for label, size in SIZES:
            for repeat in range(REPEATS):
                payload = os.urandom(size)  # fresh incompressible bytes, outside timing
                order = ("go", "js") if repeat % 2 == 0 else ("js", "go")
                for backend in order:
                    flow = Flow(backend, directory, f"{label}-{repeat}")
                    try:
                        for scenario in ("cold", "sender_cached", "receiver_cached"):
                            row = flow.send(payload, scenario)
                            row.update(size_label=label, repeat=repeat, concurrency=1)
                            record(row)
                    finally:
                        flow.close()
            for repeat in range(CONCURRENT_ROUNDS):
                payloads = [os.urandom(size) for _ in range(4)]
                order = ("js", "go") if repeat % 2 == 0 else ("go", "js")
                for backend in order:
                    flows = [Flow(backend, directory, f"{label}-parallel-{repeat}-{i}") for i in range(4)]
                    barrier = threading.Barrier(5)
                    try:
                        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor:
                            jobs = [executor.submit(flow.send, payload, "cold", barrier) for flow, payload in zip(flows, payloads)]
                            start = time.perf_counter()
                            barrier.wait()
                            results = [job.result() for job in jobs]
                            elapsed = time.perf_counter() - start
                        for i, row in enumerate(results):
                            row.update(size_label=label, repeat=repeat, concurrency=4, worker=i, batch_s=elapsed)
                            record(row)
                    finally:
                        for flow in flows:
                            flow.close()
    metadata["client_qdisc_after"] = json.loads(subprocess.check_output(["tc", "-s", "-j", "qdisc", "show", "dev", "eth0"]))
    metadata["verified_transfers"] = len(ROWS)
    (OUTPUT / f"{RATE}-metadata.json").write_text(json.dumps(metadata, indent=2))
    summary = []
    keys = sorted({(r["backend"], r["size_label"], r["scenario"], r["concurrency"]) for r in ROWS})
    for backend, label, scenario, concurrency in keys:
        rows = [r for r in ROWS if (r["backend"], r["size_label"], r["scenario"], r["concurrency"]) == (backend, label, scenario, concurrency)]
        item = dict(backend=backend, size=label, scenario=scenario, concurrency=concurrency, samples=len(rows))
        for field in ("total_s", "http_s", "upload_s", "download_s", "sender_hash_s", "receiver_work_s", "http_requests", "upload_bytes", "download_bytes"):
            item["median_" + field] = statistics.median(r[field] for r in rows)
        if concurrency > 1:
            batches = {r["repeat"]: r["batch_s"] for r in rows}
            item["median_batch_s"] = statistics.median(batches.values())
        summary.append(item)
    (OUTPUT / f"{RATE}-summary.json").write_text(json.dumps(summary, indent=2))
    print(f"BENCHMARK_COMPLETE rate={RATE} verified_transfers={len(ROWS)}", flush=True)


if __name__ == "__main__":
    main()
