"""Local demo fixtures; never connects to Cloudflare or reads real yz config."""
import json
import sys
import time
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
from urllib.parse import urlsplit

ACCOUNT = "11111111111111111111111111111111"
BUCKET = "demo-shares"
KEY = "a" * 32
SECRET = "b" * 64
DOMAIN = "files.example.com"
PORT = 8766
OBJECTS = {}


def initialize(mode):
    directory = Path(__file__).resolve().parent / (".config-" + mode)
    directory.mkdir(mode=0o700, exist_ok=True)
    config = {
        "schema_version": 1,
        "access_token": "demo-access",
        "token_expiry": (datetime.now(timezone.utc) + timedelta(days=1)).isoformat(),
    }
    if mode == "upload":
        config.update(account_id=ACCOUNT, bucket=BUCKET,
                      url_base="https://" + DOMAIN,
                      s3_access_key_id=KEY, s3_secret=SECRET)
    path = directory / "config.json"
    path.touch(mode=0o600, exist_ok=True)
    path.chmod(0o600)
    path.write_text(json.dumps(config))


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def reply(self, result):
        data = json.dumps({"success": True, "result": result}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        self.path = urlsplit(self.path).path
        if self.path == "/accounts":
            self.reply([{"id": ACCOUNT, "name": "Demo account"}])
        elif self.path == f"/accounts/{ACCOUNT}/r2/buckets":
            self.reply({"buckets": [{"name": name, "jurisdiction": "default"}
                                     for name in ["demo-shares", "demo-photos", "demo-documents"]]})
        elif self.path.endswith("/domains/custom"):
            self.reply({"domains": [{"domain": DOMAIN, "enabled": True,
                                      "status": {"ownership": "active", "ssl": "active"}}]})
        elif self.path.endswith("/domains/managed"):
            self.reply({"domain": "pub-demo.r2.dev", "enabled": False})
        elif self.path in OBJECTS:
            data = OBJECTS[self.path]
            self.send_response(200)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
        else:
            self.send_error(404)

    def do_HEAD(self):
        self.send_response(200 if self.path == "/" + BUCKET else 404)
        self.end_headers()

    def do_PUT(self):
        if not self.path.startswith("/" + BUCKET + "/"):
            self.send_error(404)
            return
        OBJECTS[self.path] = self.rfile.read(int(self.headers["Content-Length"]))
        time.sleep(0.8)
        self.send_response(200)
        self.send_header("ETag", '"demo-etag"')
        self.end_headers()


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "init":
        if sys.argv[2] not in {"setup", "upload"}:
            raise SystemExit("mode must be setup or upload")
        initialize(sys.argv[2])
    else:
        HTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
