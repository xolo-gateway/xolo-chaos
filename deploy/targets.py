#!/usr/bin/env python3
"""Requests served by each Xolo instance, according to Prometheus.

This is the check that the load balancer spreads the load: the instances are
discovered through DNS, so there is one series per replica.
"""
import json
import sys
import urllib.parse
import urllib.request

PROM = sys.argv[1] if len(sys.argv) > 1 else "http://localhost:9091"
QUERY = "sum by (instance) (xolo_http_requests_total)"


def main() -> int:
    url = PROM + "/api/v1/query?" + urllib.parse.urlencode({"query": QUERY})
    try:
        with urllib.request.urlopen(url, timeout=10) as res:
            payload = json.load(res)
    except Exception as err:
        print(f"Prometheus unreachable at {PROM}: {err}", file=sys.stderr)
        return 1

    results = payload.get("data", {}).get("result", [])
    if not results:
        print("No instance seen by Prometheus.")
        print("DNS discovery and the first scrape take about ten seconds.")
        return 0

    rows = sorted(
        (r["metric"].get("instance", "?"), float(r["value"][1])) for r in results
    )
    total = sum(n for _, n in rows)
    width = max(len(i) for i, _ in rows)
    for instance, n in rows:
        part = n / total * 100 if total else 0
        print(f"  {instance:<{width}}  {n:9,.0f} requests  {part:5.1f} %")
    print(f"  {'total':<{width}}  {total:9,.0f} requests")
    if len(rows) > 1:
        spread = (max(n for _, n in rows) - min(n for _, n in rows)) / total * 100
        print(f"\n  {len(rows)} instances, max spread between them: {spread:.1f} points")
    return 0


if __name__ == "__main__":
    sys.exit(main())
