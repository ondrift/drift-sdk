<?php
/**
 * Drift SDK for PHP Atomic functions.
 *
 * This single-file SDK provides:
 *   - \Drift\run($handler): Entry point that dispatches to deployed or local mode.
 *   - Backbone helpers: Secret, Cache, Nosql, Queue, Blob, Lock, Vector.
 *   - \Drift\log($msg): Writes to stderr (captured by the runner as function logs).
 *   - \Drift\http_request(): Outbound HTTP from within a function.
 *
 * All backbone helpers use only PHP built-ins -- zero external dependencies.
 */

namespace Drift;

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

function run(callable $handler): void {
    if (getenv('DRIFT_RUNTIME')) {
        _run_deployed($handler);
    } else {
        _run_local($handler);
    }
}

function _run_deployed(callable $handler): void {
    $input = file_get_contents('php://stdin');
    $req = json_decode($input, true);
    $resp = $handler($req);
    fwrite(STDOUT, json_encode($resp));
}

function _run_local(callable $handler): void {
    $port = (int)(getenv('PORT') ?: '8080');
    $server = stream_socket_server("tcp://0.0.0.0:$port", $errno, $errstr);
    if (!$server) {
        fwrite(STDERR, "drift-sdk: failed to start server: $errstr ($errno)\n");
        exit(1);
    }
    fwrite(STDERR, "drift-sdk: local server starting on :$port\n");

    while ($client = @stream_socket_accept($server, -1)) {
        try {
            $request_line = fgets($client);
            if (!$request_line) { fclose($client); continue; }
            $parts = explode(' ', trim($request_line));
            $method = $parts[0];
            $path_str = $parts[1] ?? '/';

            $headers = [];
            while (($line = fgets($client)) && trim($line) !== '') {
                $pair = explode(': ', trim($line), 2);
                if (count($pair) === 2) {
                    $headers[strtolower($pair[0])] = $pair[1];
                }
            }

            $body = null;
            if (isset($headers['content-length'])) {
                $raw = fread($client, (int)$headers['content-length']);
                $decoded = json_decode($raw, true);
                $body = ($decoded !== null) ? $decoded : $raw;
            }

            $parsed = parse_url($path_str);
            $req = [
                'method' => $method,
                'path' => $parsed['path'] ?? '/',
                'headers' => $headers,
                'query' => $parsed['query'] ?? '',
                'body' => $body,
            ];

            $resp = $handler($req);
            $status = $resp['status'] ?? 200;
            $out = json_encode($resp);

            $response = "HTTP/1.1 $status OK\r\n";
            $response .= "Content-Type: application/json\r\n";
            $response .= "Content-Length: " . strlen($out) . "\r\n";
            $response .= "\r\n";
            $response .= $out;

            fwrite($client, $response);
        } catch (\Throwable $e) {
            fwrite(STDERR, "drift-sdk: {$e->getMessage()}\n");
        } finally {
            @fclose($client);
        }
    }
}

// ---------------------------------------------------------------------------
// Backbone transport
// ---------------------------------------------------------------------------

$_backbone_url = null;

function _get_backbone_url(): string {
    global $_backbone_url;
    if ($_backbone_url === null) {
        $_backbone_url = getenv('BACKBONE_URL') ?: '';
    }
    return $_backbone_url;
}

function _call(string $method, string $path, $body = null) {
    $base = _get_backbone_url();
    if ($base === '') {
        return _call_local($method, $path, $body);
    }

    $url = "$base/$path";
    $opts = ['http' => ['method' => $method, 'ignore_errors' => true]];

    if ($body !== null) {
        $opts['http']['header'] = "Content-Type: application/json\r\n";
        $opts['http']['content'] = json_encode($body);
    }

    $context = stream_context_create($opts);
    $result = @file_get_contents($url, false, $context);

    if (isset($http_response_header)) {
        foreach ($http_response_header as $header) {
            if (preg_match('/^HTTP\/\S+ 204/', $header)) {
                return null;
            }
        }
    }

    if ($result === false || $result === '') return null;

    $decoded = json_decode($result, true);
    return ($decoded !== null) ? $decoded : $result;
}

// In-memory backbone for local dev.
$_local_store = [
    'nosql' => [], 'cache' => [], 'queues' => [],
    'blobs' => [], 'locks' => [], 'next_id' => 0,
];

function _call_local(string $method, string $path, $body = null) {
    global $_local_store;
    $s = &$_local_store;

    $parts = explode('?', $path, 2);
    $base_path = $parts[0];
    $query = [];
    if (isset($parts[1])) parse_str($parts[1], $query);

    // NoSQL
    if ($base_path === 'write' && $method === 'POST') {
        $col = ($body ?? [])['collection'] ?? 'default';
        if (!isset($s['nosql'][$col])) $s['nosql'][$col] = [];
        $s['next_id']++;
        $key = (string)$s['next_id'];
        $s['nosql'][$col][$key] = $body;
        return ['key' => $key];
    }
    if ($base_path === 'read' && $method === 'GET') {
        $col = $query['collection'] ?? 'default';
        return ($s['nosql'][$col] ?? [])[$query['key'] ?? ''] ?? null;
    }
    if ($base_path === 'nosql/list' && $method === 'GET') {
        $col = $query['collection'] ?? 'default';
        $docs = $s['nosql'][$col] ?? [];
        $field = $query['field'] ?? null;
        $value = $query['value'] ?? null;
        $results = [];
        foreach ($docs as $doc) {
            if ($field !== null && (string)($doc[$field] ?? '') !== $value) continue;
            $results[] = $doc;
        }
        return $results;
    }
    if ($base_path === 'nosql/drop' && $method === 'POST') {
        unset($s['nosql'][$query['collection'] ?? 'default']);
        return null;
    }

    // Cache
    if ($base_path === 'cache/set' && $method === 'POST') {
        $s['cache'][($body ?? [])['key'] ?? ''] = ($body ?? [])['value'] ?? null;
        return null;
    }
    if ($base_path === 'cache/get' && $method === 'GET') {
        return $s['cache'][$query['key'] ?? ''] ?? null;
    }
    if ($base_path === 'cache/del') {
        unset($s['cache'][$query['key'] ?? '']);
        return null;
    }

    // Queue
    if ($base_path === 'queue/push' && $method === 'POST') {
        $name = ($body ?? [])['queue'] ?? '';
        if (!isset($s['queues'][$name])) $s['queues'][$name] = [];
        $s['queues'][$name][] = ($body ?? [])['body'] ?? null;
        return null;
    }
    if ($base_path === 'queue/pop' && $method === 'POST') {
        $name = ($body ?? [])['queue'] ?? '';
        if (empty($s['queues'][$name])) return null;
        return array_shift($s['queues'][$name]);
    }

    // Blob
    if ($base_path === 'blob/put' && $method === 'POST') {
        $s['blobs'][($body ?? [])['name'] ?? ''] = ($body ?? [])['data'] ?? null;
        return null;
    }
    if ($base_path === 'blob/get' && $method === 'GET') {
        return $s['blobs'][$query['name'] ?? ''] ?? null;
    }

    // Secret
    if ($base_path === 'secret/get' && $method === 'GET') return null;

    // Lock
    if ($base_path === 'lock/acquire' && $method === 'POST') {
        $name = ($body ?? [])['name'] ?? '';
        if (isset($s['locks'][$name])) return null;
        $s['next_id']++;
        $token = "local-lock-{$s['next_id']}";
        $s['locks'][$name] = $token;
        return ['token' => $token];
    }
    if ($base_path === 'lock/release' && $method === 'POST') {
        unset($s['locks'][($body ?? [])['name'] ?? '']);
        return null;
    }

    // Vector
    if ($base_path === 'vector/insert' && $method === 'POST') return null;
    if ($base_path === 'vector/search' && $method === 'POST') return [];

    return null;
}

// ---------------------------------------------------------------------------
// Secret
// ---------------------------------------------------------------------------

class Secret {
    public static function get(string $name): string {
        $resp = _call('GET', 'secret/get?name=' . urlencode($name));
        return is_string($resp) ? $resp : ($resp !== null ? json_encode($resp) : '');
    }

    public static function set(string $name, string $value): void {
        _call('POST', 'secret/set', ['name' => $name, 'value' => $value]);
    }

    public static function delete(string $name): void {
        _call('DELETE', 'secret/delete?name=' . urlencode($name));
    }
}

// ---------------------------------------------------------------------------
// Cache
// ---------------------------------------------------------------------------

class Cache {
    public static function get(string $key) {
        return _call('GET', 'cache/get?key=' . urlencode($key));
    }

    public static function set(string $key, $value, int $ttl = 0): void {
        $payload = ['key' => $key, 'value' => $value];
        if ($ttl > 0) $payload['ttl'] = $ttl;
        _call('POST', 'cache/set', $payload);
    }

    public static function delete(string $key): void {
        _call('DELETE', 'cache/del?key=' . urlencode($key));
    }
}

// ---------------------------------------------------------------------------
// NoSQL
// ---------------------------------------------------------------------------

class Nosql {
    public static function collection(string $name): NosqlCollection {
        return new NosqlCollection($name);
    }
}

class NosqlCollection {
    private string $name;

    public function __construct(string $name) { $this->name = $name; }

    public function insert($doc): string {
        $payload = ['collection' => $this->name];
        if (is_array($doc)) {
            $payload = array_merge($payload, $doc);
        } else {
            $payload['data'] = $doc;
        }
        $resp = _call('POST', 'write', $payload);
        return is_array($resp) ? ($resp['key'] ?? '') : '';
    }

    public function read(string $key) {
        return _call('GET', 'read?collection=' . urlencode($this->name) . '&key=' . urlencode($key));
    }

    public function list(?array $filter = null): array {
        $path = 'nosql/list?collection=' . urlencode($this->name);
        if ($filter) {
            foreach ($filter as $k => $v) {
                $path .= '&field=' . urlencode($k) . '&value=' . urlencode($v);
            }
        }
        $resp = _call('GET', $path);
        return is_array($resp) ? $resp : [];
    }

    public function drop(): void {
        _call('POST', 'nosql/drop?collection=' . urlencode($this->name));
    }
}

// ---------------------------------------------------------------------------
// Queue
// ---------------------------------------------------------------------------

function queue(string $name): QueueHandle {
    return new QueueHandle($name);
}

class QueueHandle {
    private string $name;

    public function __construct(string $name) { $this->name = $name; }

    public function push($body): void {
        _call('POST', 'queue/push', ['queue' => $this->name, 'body' => $body]);
    }

    public function pop() {
        return _call('POST', 'queue/pop', ['queue' => $this->name]);
    }
}

// ---------------------------------------------------------------------------
// Blob
// ---------------------------------------------------------------------------

class Blob {
    public static function put(string $name, $data): void {
        _call('POST', 'blob/put', ['name' => $name, 'data' => $data]);
    }

    public static function get(string $name) {
        return _call('GET', 'blob/get?name=' . urlencode($name));
    }
}

// ---------------------------------------------------------------------------
// Lock
// ---------------------------------------------------------------------------

class Lock {
    public static function acquire(string $name, int $ttl = 30): string {
        $resp = _call('POST', 'lock/acquire', ['name' => $name, 'ttl' => $ttl]);
        return ($resp ?? [])['token'] ?? '';
    }

    public static function release(string $name, string $token): void {
        _call('POST', 'lock/release', ['name' => $name, 'token' => $token]);
    }
}

// ---------------------------------------------------------------------------
// Vector
// ---------------------------------------------------------------------------

class Vector {
    public static function collection(string $name): VectorCollection {
        return new VectorCollection($name);
    }
}

class VectorCollection {
    private string $name;

    public function __construct(string $name) { $this->name = $name; }

    public function insert(string $id, array $vector, ?array $metadata = null): void {
        _call('POST', 'vector/insert', [
            'collection' => $this->name, 'id' => $id,
            'vector' => $vector, 'metadata' => $metadata,
        ]);
    }

    public function search(array $vector, int $k = 10): array {
        $resp = _call('POST', 'vector/search', [
            'collection' => $this->name, 'vector' => $vector, 'k' => $k,
        ]);
        return is_array($resp) ? $resp : [];
    }
}

// ---------------------------------------------------------------------------
// Logging
// ---------------------------------------------------------------------------

function log($msg): void {
    fwrite(STDERR, strval($msg) . "\n");
}

// ---------------------------------------------------------------------------
// HTTP client
// ---------------------------------------------------------------------------

function http_request(string $method, string $url, ?array $headers = null, $body = null): array {
    $opts = ['http' => ['method' => $method, 'ignore_errors' => true]];

    $header_str = '';
    if ($headers) {
        foreach ($headers as $k => $v) {
            $header_str .= "$k: $v\r\n";
        }
    }
    if ($body !== null) {
        $opts['http']['content'] = is_string($body) ? $body : json_encode($body);
    }
    if ($header_str) $opts['http']['header'] = $header_str;

    $context = stream_context_create($opts);
    $result = @file_get_contents($url, false, $context);

    $status = 0;
    if (isset($http_response_header[0]) && preg_match('/^HTTP\/\S+ (\d+)/', $http_response_header[0], $m)) {
        $status = (int)$m[1];
    }

    return ['status' => $status, 'body' => $result ?: ''];
}
