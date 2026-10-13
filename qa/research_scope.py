"""Operator QC setup inside sandbox: temporary exact-IP rules, never allow_all."""
import ipaddress
import json
import pathlib
import urllib.parse
import urllib.request

API = "http://172.20.0.3:9080/api/v1/gateway"


def request(path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(API + path, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=15) as response:
        return json.load(response)


def main():
    state = {"before": request("/egress-mode"), "created_rules": [], "hosts": []}
    path = pathlib.Path("/home/pentester/work/research-qc-scope.json")
    path.parent.mkdir(parents=True, exist_ok=True)

    def allow(ip):
        address = ipaddress.ip_address(ip)
        if not address.is_global:
            raise ValueError("non-public destination refused")
        rule = request("/rules", {"network": str(address) + ("/32" if address.version == 4 else "/128"),
                                  "ttl_secs": 1800, "reason": "Operator-requested browser-search QC exact destination"})
        state["created_rules"].append(rule)
        path.write_text(json.dumps(state, indent=2))

    allow("1.1.1.1")
    request("/egress-mode", {"mode": "block_except_rules"})
    for host in ["openrouter.ai", "www.openssh.com", "www.qualys.com", "blog.qualys.com"]:
        ips = set()
        for kind in ["A", "AAAA"]:
            req = urllib.request.Request("https://1.1.1.1/dns-query?name=" + urllib.parse.quote(host) + "&type=" + kind,
                                         headers={"Accept": "application/dns-json"})
            with urllib.request.urlopen(req, timeout=10) as response:
                answer = json.load(response)
            for record in answer.get("Answer", []):
                if record["type"] in [1, 28]:
                    ips.add(record["data"])
        for ip in sorted(ips):
            allow(ip)
        state["hosts"].append({"host": host, "ips": sorted(ips)})
    state["after"] = request("/egress-mode")
    path.write_text(json.dumps(state, indent=2))
    print(json.dumps({"mode": state["after"], "temporary_rules": len(state["created_rules"]), "hosts": state["hosts"]}))


if __name__ == "__main__":
    main()
