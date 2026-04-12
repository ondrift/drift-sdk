//! Drift SDK for Rust Atomic functions.
//!
//! Provides:
//!   - run(handler): Entry point (deployed or local mode).
//!   - Backbone helpers: secret, cache, nosql, queue, blob, lock, vector.
//!   - log(msg): Writes to stderr (captured by runner).
//!   - http_request(): Outbound HTTP from within a function.

use std::collections::HashMap;
use std::io::{self, BufRead, BufReader, Read, Write};
use std::net::TcpListener;

pub use serde_json::Value;

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

pub fn run<F>(handler: F)
where
    F: Fn(Value) -> Value + 'static,
{
    if std::env::var("DRIFT_RUNTIME").is_ok() {
        run_deployed(&handler);
    } else {
        run_local(handler);
    }
}

fn run_deployed<F: Fn(Value) -> Value>(handler: &F) {
    let mut input = String::new();
    io::stdin().read_to_string(&mut input).unwrap();
    let req: Value = serde_json::from_str(&input).unwrap_or(Value::Null);
    let resp = handler(req);
    let out = serde_json::to_string(&resp).unwrap();
    io::stdout().write_all(out.as_bytes()).unwrap();
    io::stdout().flush().unwrap();
}

fn run_local<F: Fn(Value) -> Value + 'static>(handler: F) {
    let port: u16 = std::env::var("PORT")
        .unwrap_or_else(|_| "8080".into())
        .parse()
        .unwrap_or(8080);

    let listener = TcpListener::bind(format!("0.0.0.0:{}", port)).unwrap();
    eprintln!("drift-sdk: local server starting on :{}", port);

    for stream in listener.incoming() {
        let mut stream = match stream {
            Ok(s) => s,
            Err(_) => continue,
        };

        let mut reader = BufReader::new(match stream.try_clone() {
            Ok(s) => s,
            Err(_) => continue,
        });

        // Read request line.
        let mut request_line = String::new();
        if reader.read_line(&mut request_line).is_err() {
            continue;
        }
        let parts: Vec<&str> = request_line.trim().splitn(3, ' ').collect();
        if parts.len() < 2 {
            continue;
        }
        let method = parts[0];
        let path_str = parts[1];

        // Read headers.
        let mut headers = serde_json::Map::new();
        let mut content_length: usize = 0;
        loop {
            let mut line = String::new();
            if reader.read_line(&mut line).is_err() {
                break;
            }
            let trimmed = line.trim().to_string();
            if trimmed.is_empty() {
                break;
            }
            if let Some((k, v)) = trimmed.split_once(": ") {
                if k.eq_ignore_ascii_case("content-length") {
                    content_length = v.parse().unwrap_or(0);
                }
                headers.insert(k.to_lowercase(), Value::String(v.to_string()));
            }
        }

        // Read body.
        let body = if content_length > 0 {
            let mut buf = vec![0u8; content_length];
            let _ = reader.read_exact(&mut buf);
            let raw = String::from_utf8_lossy(&buf).to_string();
            match serde_json::from_str::<Value>(&raw) {
                Ok(v) => v,
                Err(_) => Value::String(raw),
            }
        } else {
            Value::Null
        };

        // Parse path and query.
        let (path, query) = match path_str.split_once('?') {
            Some((p, q)) => (p, q),
            None => (path_str, ""),
        };

        let req = serde_json::json!({
            "method": method,
            "path": path,
            "headers": Value::Object(headers),
            "query": query,
            "body": body,
        });

        let resp = handler(req);
        let status = resp.get("status").and_then(|s| s.as_u64()).unwrap_or(200);
        let out = serde_json::to_string(&resp).unwrap();

        let response = format!(
            "HTTP/1.1 {} OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\n\r\n{}",
            status,
            out.len(),
            out
        );
        let _ = stream.write_all(response.as_bytes());
    }
}

// ---------------------------------------------------------------------------
// Backbone transport
// ---------------------------------------------------------------------------

fn get_backbone_url() -> String {
    std::env::var("BACKBONE_URL").unwrap_or_default()
}

fn percent_encode(s: &str) -> String {
    let mut out = String::new();
    for b in s.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char);
            }
            _ => {
                out.push_str(&format!("%{:02X}", b));
            }
        }
    }
    out
}

fn call(method: &str, path: &str, body: Option<Value>) -> Option<Value> {
    let base = get_backbone_url();
    if base.is_empty() {
        return None;
    }

    let url = format!("{}/{}", base, path);

    let result = if let Some(b) = body {
        ureq::request(method, &url)
            .set("Content-Type", "application/json")
            .send_string(&serde_json::to_string(&b).unwrap_or_default())
    } else {
        ureq::request(method, &url).call()
    };

    match result {
        Ok(resp) => {
            if resp.status() == 204 {
                return None;
            }
            let text = resp.into_string().unwrap_or_default();
            if text.is_empty() {
                return None;
            }
            serde_json::from_str(&text)
                .ok()
                .or_else(|| Some(Value::String(text)))
        }
        Err(ureq::Error::Status(204, _)) => None,
        Err(ureq::Error::Status(_, resp)) => {
            let text = resp.into_string().unwrap_or_default();
            if text.is_empty() {
                return None;
            }
            serde_json::from_str(&text)
                .ok()
                .or_else(|| Some(Value::String(text)))
        }
        Err(_) => None,
    }
}

// ---------------------------------------------------------------------------
// Secret
// ---------------------------------------------------------------------------

pub mod secret {
    use super::*;

    pub fn get(name: &str) -> String {
        match call("GET", &format!("secret/get?name={}", percent_encode(name)), None) {
            Some(Value::String(s)) => s,
            Some(v) => serde_json::to_string(&v).unwrap_or_default(),
            None => String::new(),
        }
    }

    pub fn set(name: &str, value: &str) {
        call("POST", "secret/set", Some(serde_json::json!({"name": name, "value": value})));
    }

    pub fn delete(name: &str) {
        call("DELETE", &format!("secret/delete?name={}", percent_encode(name)), None);
    }
}

// ---------------------------------------------------------------------------
// Cache
// ---------------------------------------------------------------------------

pub mod cache {
    use super::*;

    pub fn get(key: &str) -> Option<Value> {
        call("GET", &format!("cache/get?key={}", percent_encode(key)), None)
    }

    pub fn set(key: &str, value: Value, ttl: u64) {
        let mut payload = serde_json::json!({"key": key, "value": value});
        if ttl > 0 {
            payload["ttl"] = Value::from(ttl);
        }
        call("POST", "cache/set", Some(payload));
    }

    pub fn delete(key: &str) {
        call("DELETE", &format!("cache/del?key={}", percent_encode(key)), None);
    }
}

// ---------------------------------------------------------------------------
// NoSQL
// ---------------------------------------------------------------------------

pub mod nosql {
    use super::*;

    pub struct Collection {
        name: String,
    }

    pub fn collection(name: &str) -> Collection {
        Collection { name: name.to_string() }
    }

    impl Collection {
        pub fn insert(&self, doc: Value) -> String {
            let mut payload = serde_json::json!({"collection": self.name});
            if let Value::Object(map) = doc {
                for (k, v) in map {
                    payload[&k] = v;
                }
            } else {
                payload["data"] = doc;
            }
            match call("POST", "write", Some(payload)) {
                Some(Value::Object(m)) => {
                    m.get("key").and_then(|v| v.as_str()).unwrap_or("").to_string()
                }
                _ => String::new(),
            }
        }

        pub fn read(&self, key: &str) -> Option<Value> {
            call("GET", &format!("read?collection={}&key={}", percent_encode(&self.name), percent_encode(key)), None)
        }

        pub fn list(&self, filter: Option<HashMap<String, String>>) -> Vec<Value> {
            let mut path = format!("nosql/list?collection={}", percent_encode(&self.name));
            if let Some(f) = filter {
                for (k, v) in f {
                    path.push_str(&format!("&field={}&value={}", percent_encode(&k), percent_encode(&v)));
                }
            }
            match call("GET", &path, None) {
                Some(Value::Array(arr)) => arr,
                _ => vec![],
            }
        }

        pub fn drop(&self) {
            call("POST", &format!("nosql/drop?collection={}", percent_encode(&self.name)), None);
        }
    }
}

// ---------------------------------------------------------------------------
// Queue
// ---------------------------------------------------------------------------

pub struct QueueHandle {
    name: String,
}

pub fn queue(name: &str) -> QueueHandle {
    QueueHandle { name: name.to_string() }
}

impl QueueHandle {
    pub fn push(&self, body: Value) {
        call("POST", "queue/push", Some(serde_json::json!({"queue": self.name, "body": body})));
    }

    pub fn pop(&self) -> Option<Value> {
        call("POST", "queue/pop", Some(serde_json::json!({"queue": self.name})))
    }
}

// ---------------------------------------------------------------------------
// Blob
// ---------------------------------------------------------------------------

pub mod blob {
    use super::*;

    pub fn put(name: &str, data: Value) {
        call("POST", "blob/put", Some(serde_json::json!({"name": name, "data": data})));
    }

    pub fn get(name: &str) -> Option<Value> {
        call("GET", &format!("blob/get?name={}", percent_encode(name)), None)
    }
}

// ---------------------------------------------------------------------------
// Lock
// ---------------------------------------------------------------------------

pub mod lock {
    use super::*;

    pub fn acquire(name: &str, ttl: u64) -> String {
        match call("POST", "lock/acquire", Some(serde_json::json!({"name": name, "ttl": ttl}))) {
            Some(Value::Object(m)) => {
                m.get("token").and_then(|v| v.as_str()).unwrap_or("").to_string()
            }
            _ => String::new(),
        }
    }

    pub fn release(name: &str, token: &str) {
        call("POST", "lock/release", Some(serde_json::json!({"name": name, "token": token})));
    }
}

// ---------------------------------------------------------------------------
// Vector
// ---------------------------------------------------------------------------

pub mod vector {
    use super::*;

    pub struct VectorCollection {
        name: String,
    }

    pub fn collection(name: &str) -> VectorCollection {
        VectorCollection { name: name.to_string() }
    }

    impl VectorCollection {
        pub fn insert(&self, id: &str, vector: Vec<f64>, metadata: Option<Value>) {
            call("POST", "vector/insert", Some(serde_json::json!({
                "collection": self.name, "id": id,
                "vector": vector, "metadata": metadata,
            })));
        }

        pub fn search(&self, vector: Vec<f64>, k: u32) -> Vec<Value> {
            match call("POST", "vector/search", Some(serde_json::json!({
                "collection": self.name, "vector": vector, "k": k,
            }))) {
                Some(Value::Array(arr)) => arr,
                _ => vec![],
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Logging
// ---------------------------------------------------------------------------

pub fn log(msg: &str) {
    eprintln!("{}", msg);
}

// ---------------------------------------------------------------------------
// HTTP client
// ---------------------------------------------------------------------------

pub fn http_request(
    method: &str,
    url: &str,
    headers: Option<HashMap<String, String>>,
    body: Option<&str>,
) -> (u16, String) {
    let mut req = ureq::request(method, url);

    if let Some(hdrs) = headers {
        for (k, v) in hdrs {
            req = req.set(&k, &v);
        }
    }

    let result = if let Some(b) = body {
        req.send_string(b)
    } else {
        req.call()
    };

    match result {
        Ok(r) => {
            let status = r.status();
            let text = r.into_string().unwrap_or_default();
            (status, text)
        }
        Err(ureq::Error::Status(code, resp)) => {
            let text = resp.into_string().unwrap_or_default();
            (code, text)
        }
        Err(_) => (0, String::new()),
    }
}
