"""
Drift SDK for Python Atomic functions.

This single-file SDK provides:
  - run(handler): Entry point that dispatches to deployed or local mode.
  - Backbone helpers: secret, cache, nosql, queue, blob, lock, vector.
  - log(msg): Writes to stderr (captured by the runner as function logs).
  - http_request(): Outbound HTTP from within a function.

All backbone helpers use only stdlib (urllib.request) -- zero external dependencies.
"""

import json
import os
import sys
import urllib.request
import urllib.parse
import urllib.error
from http.server import HTTPServer, BaseHTTPRequestHandler

# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def run(handler):
    """Entry point for Drift Atomic functions.

    In deployed mode (DRIFT_RUNTIME is set): reads JSON from stdin,
    calls handler, writes JSON to stdout.

    In local dev mode: starts an HTTP server on PORT (default 8080).
    """
    if os.environ.get("DRIFT_RUNTIME"):
        _run_deployed(handler)
    else:
        _run_local(handler)


def _run_deployed(handler):
    req = json.loads(sys.stdin.read())
    resp = handler(req)
    sys.stdout.write(json.dumps(resp))
    sys.stdout.flush()


def _run_local(handler):
    port = int(os.environ.get("PORT", "8080"))

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            self._handle()

        def do_POST(self):
            self._handle()

        def do_PUT(self):
            self._handle()

        def do_DELETE(self):
            self._handle()

        def _handle(self):
            content_length = int(self.headers.get("Content-Length", 0))
            body = None
            if content_length > 0:
                raw = self.rfile.read(content_length)
                try:
                    body = json.loads(raw)
                except json.JSONDecodeError:
                    body = raw.decode("utf-8", errors="replace")

            parsed = urllib.parse.urlparse(self.path)
            headers = {k: self.headers[k] for k in self.headers}

            req = {
                "method": self.command,
                "path": parsed.path,
                "headers": headers,
                "query": parsed.query,
                "body": body,
            }

            resp = handler(req)
            status = resp.get("status", 200)
            out = json.dumps(resp).encode("utf-8")

            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(out)

        def log_message(self, fmt, *args):
            sys.stderr.write(f"drift-sdk: {fmt % args}\n")

    server = HTTPServer(("", port), Handler)
    sys.stderr.write(f"drift-sdk: local server starting on :{port}\n")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass


# ---------------------------------------------------------------------------
# Backbone transport
# ---------------------------------------------------------------------------

_backbone_url = None


def _get_backbone_url():
    global _backbone_url
    if _backbone_url is None:
        _backbone_url = os.environ.get("BACKBONE_URL", "")
    return _backbone_url


def _call(method, path, body=None):
    """Call backbone via HTTP (deployed) or return None (local dev fallback)."""
    base = _get_backbone_url()
    if not base:
        return _call_local(method, path, body)

    url = f"{base}/{path}"
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"

    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            raw = resp.read()
            if not raw:
                return None
            try:
                return json.loads(raw)
            except (json.JSONDecodeError, ValueError):
                return raw.decode("utf-8", errors="replace")
    except urllib.error.HTTPError as e:
        if e.code == 204:
            return None
        raise


# In-memory backbone for local dev (matches Go SDK behavior).
_local_store = {
    "nosql": {},
    "cache": {},
    "queues": {},
    "blobs": {},
    "locks": {},
    "next_id": 0,
}


def _call_local(method, path, body=None):
    """In-memory backbone for local development."""
    s = _local_store
    base_path = path.split("?")[0]
    query = {}
    if "?" in path:
        query = dict(urllib.parse.parse_qsl(path.split("?", 1)[1]))

    # NoSQL
    if base_path == "write" and method == "POST":
        col = (body or {}).get("collection", "default")
        if col not in s["nosql"]:
            s["nosql"][col] = {}
        s["next_id"] += 1
        key = str(s["next_id"])
        s["nosql"][col][key] = body
        return {"key": key}

    if base_path == "read" and method == "GET":
        col = query.get("collection", "default")
        key = query.get("key", "")
        return s["nosql"].get(col, {}).get(key)

    if base_path == "nosql/list" and method == "GET":
        col = query.get("collection", "default")
        docs = s["nosql"].get(col, {})
        field = query.get("field")
        value = query.get("value")
        results = []
        for doc in docs.values():
            if field and str(doc.get(field)) != value:
                continue
            results.append(doc)
        return results

    if base_path == "nosql/drop" and method == "POST":
        col = query.get("collection", "default")
        s["nosql"].pop(col, None)
        return None

    # Cache
    if base_path == "cache/set" and method == "POST":
        s["cache"][(body or {}).get("key", "")] = (body or {}).get("value")
        return None

    if base_path == "cache/get" and method == "GET":
        return s["cache"].get(query.get("key", ""))

    if base_path == "cache/del":
        s["cache"].pop(query.get("key", ""), None)
        return None

    # Queue
    if base_path == "queue/push" and method == "POST":
        name = (body or {}).get("queue", "")
        msg = (body or {}).get("body")
        s["queues"].setdefault(name, []).append(msg)
        return None

    if base_path == "queue/pop" and method == "POST":
        name = (body or {}).get("queue", "")
        q = s["queues"].get(name, [])
        if not q:
            return None
        return q.pop(0)

    # Blob
    if base_path == "blob/put" and method == "POST":
        s["blobs"][(body or {}).get("name", "")] = (body or {}).get("data")
        return None

    if base_path == "blob/get" and method == "GET":
        return s["blobs"].get(query.get("name", ""))

    # Secret
    if base_path == "secret/get" and method == "GET":
        return None

    # Lock
    if base_path == "lock/acquire" and method == "POST":
        name = (body or {}).get("name", "")
        if name in s["locks"]:
            return None
        s["next_id"] += 1
        token = f"local-lock-{s['next_id']}"
        s["locks"][name] = token
        return {"token": token}

    if base_path == "lock/release" and method == "POST":
        s["locks"].pop((body or {}).get("name", ""), None)
        return None

    # Vector
    if base_path == "vector/insert" and method == "POST":
        return None

    if base_path == "vector/search" and method == "POST":
        return []

    return None


# ---------------------------------------------------------------------------
# Secret
# ---------------------------------------------------------------------------

class _SecretNS:
    def get(self, name):
        resp = _call("GET", f"secret/get?name={urllib.parse.quote(name)}")
        return resp if isinstance(resp, str) else (json.dumps(resp) if resp else "")

    def set(self, name, value):
        _call("POST", "secret/set", {"name": name, "value": value})

    def delete(self, name):
        _call("DELETE", f"secret/delete?name={urllib.parse.quote(name)}")

secret = _SecretNS()


# ---------------------------------------------------------------------------
# Cache
# ---------------------------------------------------------------------------

class _CacheNS:
    def get(self, key):
        return _call("GET", f"cache/get?key={urllib.parse.quote(key)}")

    def set(self, key, value, ttl=0):
        payload = {"key": key, "value": value}
        if ttl > 0:
            payload["ttl"] = ttl
        _call("POST", "cache/set", payload)

    def delete(self, key):
        _call("DELETE", f"cache/del?key={urllib.parse.quote(key)}")

cache = _CacheNS()


# ---------------------------------------------------------------------------
# NoSQL
# ---------------------------------------------------------------------------

class _NoSQLNS:
    def collection(self, name):
        return _CollectionHandle(name)

class _CollectionHandle:
    def __init__(self, name):
        self.name = name

    def insert(self, doc):
        payload = {"collection": self.name}
        if isinstance(doc, dict):
            payload.update(doc)
        else:
            payload["data"] = doc
        resp = _call("POST", "write", payload)
        if isinstance(resp, dict):
            return resp.get("key", "")
        return ""

    def read(self, key):
        return _call("GET", f"read?collection={urllib.parse.quote(self.name)}&key={urllib.parse.quote(key)}")

    def list(self, filter=None):
        path = f"nosql/list?collection={urllib.parse.quote(self.name)}"
        if filter:
            for k, v in filter.items():
                path += f"&field={urllib.parse.quote(k)}&value={urllib.parse.quote(v)}"
        resp = _call("GET", path)
        return resp if isinstance(resp, list) else []

    def drop(self):
        _call("POST", f"nosql/drop?collection={urllib.parse.quote(self.name)}")

nosql = _NoSQLNS()


# ---------------------------------------------------------------------------
# Queue
# ---------------------------------------------------------------------------

class _QueueHandle:
    def __init__(self, name):
        self.name = name

    def push(self, body):
        _call("POST", "queue/push", {"queue": self.name, "body": body})

    def pop(self):
        return _call("POST", "queue/pop", {"queue": self.name})

def queue(name):
    return _QueueHandle(name)


# ---------------------------------------------------------------------------
# Blob
# ---------------------------------------------------------------------------

class _BlobNS:
    def put(self, name, data):
        _call("POST", "blob/put", {"name": name, "data": data})

    def get(self, name):
        return _call("GET", f"blob/get?name={urllib.parse.quote(name)}")

blob = _BlobNS()


# ---------------------------------------------------------------------------
# Lock
# ---------------------------------------------------------------------------

class _LockNS:
    def acquire(self, name, ttl=30):
        resp = _call("POST", "lock/acquire", {"name": name, "ttl": ttl})
        return (resp or {}).get("token", "")

    def release(self, name, token):
        _call("POST", "lock/release", {"name": name, "token": token})

lock = _LockNS()


# ---------------------------------------------------------------------------
# Vector
# ---------------------------------------------------------------------------

class _VectorNS:
    def collection(self, name):
        return _VectorCollectionHandle(name)

class _VectorCollectionHandle:
    def __init__(self, name):
        self.name = name

    def insert(self, id, vector, metadata=None):
        _call("POST", "vector/insert", {
            "collection": self.name,
            "id": id,
            "vector": vector,
            "metadata": metadata,
        })

    def search(self, vector, k=10):
        resp = _call("POST", "vector/search", {
            "collection": self.name,
            "vector": vector,
            "k": k,
        })
        return resp if isinstance(resp, list) else []

vector = _VectorNS()


# ---------------------------------------------------------------------------
# Logging
# ---------------------------------------------------------------------------

def log(msg):
    """Write a log message to stderr (captured by the runner)."""
    sys.stderr.write(str(msg) + "\n")
    sys.stderr.flush()


# ---------------------------------------------------------------------------
# HTTP client
# ---------------------------------------------------------------------------

def http_request(method, url, headers=None, body=None):
    """Make an outbound HTTP request. Returns (status, body_bytes)."""
    data = body if isinstance(body, bytes) else (body.encode("utf-8") if body else None)
    req = urllib.request.Request(url, data=data, headers=headers or {}, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, resp.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()
