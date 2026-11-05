"""Render the saved benchmark summaries without rerunning network transfers."""
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent
RESULTS = ROOT / "results"
SIZES = ["100KiB", "1MiB", "10MiB"]


def main():
    lines = [
        "# Go＋NetLFS 与 JS＋Express：10 / 200 Mbps 本机对比", "",
        "## 测试范围", "",
        "真实 Go/JS HTTP 服务；Python 模拟客户端缓存流程，**不是 GMod 实机端到端结果**。",
        "没有 net/Protobuf、游戏帧调度、序列化、TLS 或公网 RTT；Python 哈希开销不代表 GLua。",
        "两服务各限制 1 CPU / 1 GiB；数据和 token 默认 24 小时。",
        "tc TBF 分别限制上传端与下载服务端 egress，四路并发共享每个方向的限额；burst 16 KiB。",
        "本机 AMD Ryzen 7 5700U；缓存文件使用 `/tmp` tmpfs，缓存命中数据不是物理磁盘读取性能。",
        "文件为随机二进制，每次校验哈希及完整字节一致性。全部大于 64000 B，均走 HTTP。",
        "单发各 3 次取中位数；并发各 2 批、每批 4 个独立上传＋下载，取批次耗时中位数。",
        "Go 服务使用本轮 LRU；JS 服务使用原始自托管实现（本地 TTL 已改为 24h）。",
        "负载未触发容量淘汰，因此这里不衡量淘汰压力下的吞吐；LRU 正确性由单元／竞态测试验证。", "",
    ]
    total_verified = 0
    for rate in ("10mbit", "200mbit"):
        summary = json.loads((RESULTS / f"{rate}-summary.json").read_text())
        metadata = json.loads((RESULTS / f"{rate}-metadata.json").read_text())
        total_verified += metadata["verified_transfers"]
        lookup = {(s["backend"], s["size"], s["scenario"], s["concurrency"]): s for s in summary}

        def pair(size, scenario, concurrency=1):
            return [lookup[(b, size, scenario, concurrency)] for b in ("go", "js")]

        lines.extend([f"## {rate.replace('mbit', ' Mbps')}", "", "### 首次发送：纯 HTTP 上传＋下载", "",
                      "排除 Express 150 ms 通知等待、哈希和文件写入。这是服务传输的主要比较表。", "",
                      "| 文件 | Go HTTP (s) | JS HTTP (s) | Go 上传 / 下载 (s) | JS 上传 / 下载 (s) |",
                      "|---|---:|---:|---:|---:|"])
        for size in SIZES:
            go, js = pair(size, "cold")
            lines.append(f"| {size} | {go['median_http_s']:.4f} | {js['median_http_s']:.4f} | {go['median_upload_s']:.4f} / {go['median_download_s']:.4f} | {js['median_upload_s']:.4f} / {js['median_download_s']:.4f} |")
        lines.extend(["", "### 首次发送：包含客户端策略的流程耗时", "",
                      "包含模拟哈希/缓存写入；Express 保留默认 150 ms 通知等待。它不是 JS 服务处理时间。", "",
                      "| 文件 | Go＋NetLFS 模型 (s) | JS＋Express 模型 (s) |", "|---|---:|---:|"])
        for size in SIZES:
            go, js = pair(size, "cold")
            lines.append(f"| {size} | {go['median_total_s']:.4f} | {js['median_total_s']:.4f} |")
        lines.extend(["", "### 上传缓存命中、接收者没有缓存", "",
                      "复用上传 ID；这里只比较 HTTP 下载，不含通知等待。", "",
                      "| 文件 | Go HTTP 下载 (s) | JS HTTP 下载 (s) |", "|---|---:|---:|"])
        for size in SIZES:
            go, js = pair(size, "sender_cached")
            lines.append(f"| {size} | {go['median_http_s']:.4f} | {js['median_http_s']:.4f} |")
        lines.extend(["", "### 同一接收者重复接收", "",
                      "NetLFS 模型读本地文件并校验；Express 模型再次 HTTP 下载，且保留 150 ms 等待。", "",
                      "| 文件 | NetLFS 本地缓存 (ms) | Express (ms) | HTTP 请求数 Go / JS | 正文下载字节 Go / JS |",
                      "|---|---:|---:|---:|---:|"])
        for size in SIZES:
            go, js = pair(size, "receiver_cached")
            lines.append(f"| {size} | {go['median_total_s']*1000:.3f} | {js['median_total_s']*1000:.3f} | {int(go['median_http_requests'])} / {int(js['median_http_requests'])} | {int(go['median_download_bytes'])} / {int(js['median_download_bytes'])} |")
        lines.extend(["", "### 四路独立冷传输：全部完成耗时", "",
                      "共享每个方向的带宽上限，不是每路独占 10/200 Mbps。包含模型开销和 Express 等待。", "",
                      "| 每路文件 | Go 四路完成 (s) | JS 四路完成 (s) |", "|---|---:|---:|"])
        for size in SIZES:
            go, js = pair(size, "cold", 4)
            lines.append(f"| {size} | {go['median_batch_s']:.4f} | {js['median_batch_s']:.4f} |")
        lines.extend(["", f"本限速组校验成功的传输/缓存交付：**{metadata['verified_transfers']}**。", ""])
    lines.extend(["## 验证与解读限制", "",
                  f"- 两组共 **{total_verified}** 次传输/缓存交付，全部校验通过。",
                  "- Go 单元测试、`go test -race`、`go vet`、格式检查和 Docker 构建通过。",
                  "- 实际 NetLFS 源码隔离探针通过：空过滤不查 size、自定义允许/拒绝、缓存命中、缺失 size。",
                  "- 所有运行使用相同服务资源限制，但这不是极限 RPS/长期压测，小样本不足以证明微小性能差异。",
                  "- 200 Mbps 小文件耗时受 TCP burst、调度和固定开销影响；不要从单个样本反推严格峰值带宽。",
                  "- 缓存命中的巨大收益主要来自省去传输，不能归因于 Go。",
                  "- 容器/临时网络由 orchestrator 清理；未修改宿主机 qdisc，未部署线上。", "",
                  "详细方法与复现命令：[README.md](README.md)。原始结果：[results/](results/)。"])
    (ROOT / "RESULTS.md").write_text("\n".join(lines) + "\n")
    print("\n".join(lines))


if __name__ == "__main__":
    main()
