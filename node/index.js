/**
 * Drift SDK for Node.js Atomic functions.
 *
 * Provides:
 *   - run(handler): Entry point (deployed or local mode).
 *   - Backbone helpers: secret, cache, nosql, queue, blob, lock, vector.
 *   - log(msg): Writes to stderr (captured by runner).
 *   - httpRequest(): Outbound HTTP from within a function.
 *
 * Uses only built-in APIs (process, fetch, http). Zero dependencies.
 */

"use strict";

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

function run(handler) {
  if (process.env.DRIFT_RUNTIME) {
    _runDeployed(handler);
  } else {
    _runLocal(handler);
  }
}

function _runDeployed(handler) {
  let data = "";
  process.stdin.on("data", (chunk) => (data += chunk));
  process.stdin.on("end", async () => {
    try {
      const req = JSON.parse(data);
      const resp = await handler(req);
      process.stdout.write(JSON.stringify(resp));
    } catch (err) {
      process.stdout.write(
        JSON.stringify({ status: 500, message: String(err), payload: null })
      );
    }
  });
}

function _runLocal(handler) {
  const http = require("http");
  const port = parseInt(process.env.PORT || "8080", 10);

  const server = http.createServer(async (req, res) => {
    let body = "";
    for await (const chunk of req) body += chunk;

    let parsed = null;
    if (body) {
      try {
        parsed = JSON.parse(body);
      } catch {
        parsed = body;
      }
    }

    const url = new URL(req.url, `http://localhost:${port}`);
    const headers = {};
    for (const [k, v] of Object.entries(req.headers)) {
      headers[k] = Array.isArray(v) ? v[0] : v;
    }

    const funcReq = {
      method: req.method,
      path: url.pathname,
      headers,
      query: url.search ? url.search.slice(1) : "",
      body: parsed,
    };

    try {
      const resp = await handler(funcReq);
      const out = JSON.stringify(resp);
      res.writeHead(resp.status || 200, { "Content-Type": "application/json" });
      res.end(out);
    } catch (err) {
      res.writeHead(500, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ status: 500, message: String(err) }));
    }
  });

  server.listen(port, () => {
    process.stderr.write(`drift-sdk: local server starting on :${port}\n`);
  });
}

// ---------------------------------------------------------------------------
// Backbone transport
// ---------------------------------------------------------------------------

let _backboneUrl = null;

function _getBackboneUrl() {
  if (_backboneUrl === null) {
    _backboneUrl = process.env.BACKBONE_URL || "";
  }
  return _backboneUrl;
}

async function _call(method, path, body) {
  const base = _getBackboneUrl();
  if (!base) return _callLocal(method, path, body);

  const url = `${base}/${path}`;
  const opts = { method };
  if (body !== undefined && body !== null) {
    opts.headers = { "Content-Type": "application/json" };
    opts.body = JSON.stringify(body);
  }

  const resp = await fetch(url, opts);
  if (resp.status === 204) return null;
  const text = await resp.text();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

// In-memory backbone for local dev.
const _store = {
  nosql: {},
  cache: {},
  queues: {},
  blobs: {},
  locks: {},
  nextId: 0,
};

function _callLocal(method, path, body) {
  const [basePath, qs] = path.split("?", 2);
  const query = {};
  if (qs) {
    for (const pair of qs.split("&")) {
      const [k, v] = pair.split("=", 2);
      query[decodeURIComponent(k)] = decodeURIComponent(v || "");
    }
  }

  // NoSQL
  if (basePath === "write" && method === "POST") {
    const col = (body && body.collection) || "default";
    if (!_store.nosql[col]) _store.nosql[col] = {};
    _store.nextId++;
    const key = String(_store.nextId);
    _store.nosql[col][key] = body;
    return { key };
  }
  if (basePath === "read" && method === "GET") {
    const col = query.collection || "default";
    return (_store.nosql[col] || {})[query.key] || null;
  }
  if (basePath === "nosql/list" && method === "GET") {
    const col = query.collection || "default";
    const docs = _store.nosql[col] || {};
    const results = [];
    for (const doc of Object.values(docs)) {
      if (query.field && String(doc[query.field]) !== query.value) continue;
      results.push(doc);
    }
    return results;
  }
  if (basePath === "nosql/drop" && method === "POST") {
    delete _store.nosql[query.collection];
    return null;
  }

  // Cache
  if (basePath === "cache/set" && method === "POST") {
    _store.cache[(body && body.key) || ""] = body && body.value;
    return null;
  }
  if (basePath === "cache/get" && method === "GET") {
    return _store.cache[query.key] !== undefined ? _store.cache[query.key] : null;
  }
  if (basePath === "cache/del") {
    delete _store.cache[query.key];
    return null;
  }

  // Queue
  if (basePath === "queue/push" && method === "POST") {
    const name = (body && body.queue) || "";
    if (!_store.queues[name]) _store.queues[name] = [];
    _store.queues[name].push(body && body.body);
    return null;
  }
  if (basePath === "queue/pop" && method === "POST") {
    const name = (body && body.queue) || "";
    const q = _store.queues[name] || [];
    if (q.length === 0) return null;
    return q.shift();
  }

  // Blob
  if (basePath === "blob/put" && method === "POST") {
    _store.blobs[(body && body.name) || ""] = body && body.data;
    return null;
  }
  if (basePath === "blob/get" && method === "GET") {
    return _store.blobs[query.name] !== undefined ? _store.blobs[query.name] : null;
  }

  // Secret
  if (basePath === "secret/get" && method === "GET") return null;

  // Lock
  if (basePath === "lock/acquire" && method === "POST") {
    const name = (body && body.name) || "";
    if (_store.locks[name]) return null;
    _store.nextId++;
    const token = `local-lock-${_store.nextId}`;
    _store.locks[name] = token;
    return { token };
  }
  if (basePath === "lock/release" && method === "POST") {
    delete _store.locks[(body && body.name) || ""];
    return null;
  }

  // Vector
  if (basePath === "vector/insert" && method === "POST") return null;
  if (basePath === "vector/search" && method === "POST") return [];

  return null;
}

// ---------------------------------------------------------------------------
// Secret
// ---------------------------------------------------------------------------

const secret = {
  get: async (name) => {
    const resp = await _call("GET", `secret/get?name=${encodeURIComponent(name)}`);
    return typeof resp === "string" ? resp : resp ? JSON.stringify(resp) : "";
  },
  set: (name, value) => _call("POST", "secret/set", { name, value }),
  delete: (name) => _call("DELETE", `secret/delete?name=${encodeURIComponent(name)}`),
};

// ---------------------------------------------------------------------------
// Cache
// ---------------------------------------------------------------------------

const cache = {
  get: (key) => _call("GET", `cache/get?key=${encodeURIComponent(key)}`),
  set: (key, value, ttl = 0) => {
    const payload = { key, value };
    if (ttl > 0) payload.ttl = ttl;
    return _call("POST", "cache/set", payload);
  },
  delete: (key) => _call("DELETE", `cache/del?key=${encodeURIComponent(key)}`),
};

// ---------------------------------------------------------------------------
// NoSQL
// ---------------------------------------------------------------------------

const nosql = {
  collection: (name) => ({
    insert: async (doc) => {
      const payload = { collection: name, ...(typeof doc === "object" ? doc : { data: doc }) };
      const resp = await _call("POST", "write", payload);
      return (resp && resp.key) || "";
    },
    read: (key) =>
      _call("GET", `read?collection=${encodeURIComponent(name)}&key=${encodeURIComponent(key)}`),
    list: (filter) => {
      let path = `nosql/list?collection=${encodeURIComponent(name)}`;
      if (filter) {
        for (const [k, v] of Object.entries(filter)) {
          path += `&field=${encodeURIComponent(k)}&value=${encodeURIComponent(v)}`;
        }
      }
      return _call("GET", path).then((r) => (Array.isArray(r) ? r : []));
    },
    drop: () => _call("POST", `nosql/drop?collection=${encodeURIComponent(name)}`),
  }),
};

// ---------------------------------------------------------------------------
// Queue
// ---------------------------------------------------------------------------

function queue(name) {
  return {
    push: (body) => _call("POST", "queue/push", { queue: name, body }),
    pop: () => _call("POST", "queue/pop", { queue: name }),
  };
}

// ---------------------------------------------------------------------------
// Blob
// ---------------------------------------------------------------------------

const blob = {
  put: (name, data) => _call("POST", "blob/put", { name, data }),
  get: (name) => _call("GET", `blob/get?name=${encodeURIComponent(name)}`),
};

// ---------------------------------------------------------------------------
// Lock
// ---------------------------------------------------------------------------

const lock = {
  acquire: async (name, ttl = 30) => {
    const resp = await _call("POST", "lock/acquire", { name, ttl });
    return (resp && resp.token) || "";
  },
  release: (name, token) => _call("POST", "lock/release", { name, token }),
};

// ---------------------------------------------------------------------------
// Vector
// ---------------------------------------------------------------------------

const vector = {
  collection: (name) => ({
    insert: (id, vec, metadata) =>
      _call("POST", "vector/insert", { collection: name, id, vector: vec, metadata }),
    search: (vec, k = 10) =>
      _call("POST", "vector/search", { collection: name, vector: vec, k }).then((r) =>
        Array.isArray(r) ? r : []
      ),
  }),
};

// ---------------------------------------------------------------------------
// Logging
// ---------------------------------------------------------------------------

function log(msg) {
  process.stderr.write(String(msg) + "\n");
}

// ---------------------------------------------------------------------------
// HTTP client
// ---------------------------------------------------------------------------

async function httpRequest(method, url, headers, body) {
  const opts = { method, headers: headers || {} };
  if (body !== undefined && body !== null) {
    opts.body = typeof body === "string" ? body : JSON.stringify(body);
  }
  const resp = await fetch(url, opts);
  const data = await resp.arrayBuffer();
  return { status: resp.status, body: Buffer.from(data) };
}

// ---------------------------------------------------------------------------
// Exports
// ---------------------------------------------------------------------------

module.exports = {
  run,
  secret,
  cache,
  nosql,
  queue,
  blob,
  lock,
  vector,
  log,
  httpRequest,
};
