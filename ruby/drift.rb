# Drift SDK for Ruby Atomic functions.
#
# This single-file SDK provides:
#   - Drift.run(handler): Entry point that dispatches to deployed or local mode.
#   - Backbone helpers: Secret, Cache, Nosql, Queue, Blob, Lock, Vector.
#   - Drift.log(msg): Writes to stderr (captured by the runner as function logs).
#   - Drift.http_request(): Outbound HTTP from within a function.
#
# All backbone helpers use only stdlib (net/http) -- zero external dependencies.

require 'json'
require 'net/http'
require 'uri'
require 'socket'

module Drift

  # ---------------------------------------------------------------------------
  # Entry point
  # ---------------------------------------------------------------------------

  def self.run(handler)
    if ENV['DRIFT_RUNTIME']
      _run_deployed(handler)
    else
      _run_local(handler)
    end
  end

  def self._run_deployed(handler)
    req = JSON.parse($stdin.read)
    resp = handler.call(req)
    $stdout.write(JSON.generate(resp))
    $stdout.flush
  end

  def self._run_local(handler)
    port = (ENV['PORT'] || '8080').to_i
    server = TCPServer.new('', port)
    $stderr.puts "drift-sdk: local server starting on :#{port}"

    loop do
      client = server.accept
      begin
        request_line = client.gets
        next unless request_line
        method, path_str, _ = request_line.strip.split(' ')

        headers = {}
        while (line = client.gets) && line.strip != ''
          key, value = line.strip.split(': ', 2)
          headers[key.downcase] = value if key
        end

        body = nil
        if headers['content-length']
          raw = client.read(headers['content-length'].to_i)
          begin
            body = JSON.parse(raw)
          rescue JSON::ParserError
            body = raw
          end
        end

        parsed = URI.parse(path_str || '/')
        req = {
          'method' => method,
          'path' => parsed.path,
          'headers' => headers,
          'query' => parsed.query || '',
          'body' => body,
        }

        resp = handler.call(req)
        status = resp['status'] || 200
        out = JSON.generate(resp)

        client.print "HTTP/1.1 #{status} OK\r\n"
        client.print "Content-Type: application/json\r\n"
        client.print "Content-Length: #{out.bytesize}\r\n"
        client.print "\r\n"
        client.print out
      rescue => e
        $stderr.puts "drift-sdk: #{e.message}"
      ensure
        client.close rescue nil
      end
    end
  end

  # ---------------------------------------------------------------------------
  # Backbone transport
  # ---------------------------------------------------------------------------

  @backbone_url = nil

  def self._get_backbone_url
    @backbone_url ||= ENV['BACKBONE_URL'] || ''
  end

  def self._call(method, path, body = nil)
    base = _get_backbone_url
    return _call_local(method, path, body) if base.empty?

    uri = URI("#{base}/#{path}")
    http = Net::HTTP.new(uri.host, uri.port)
    http.use_ssl = uri.scheme == 'https'

    request = case method
    when 'GET'    then Net::HTTP::Get.new(uri)
    when 'POST'   then Net::HTTP::Post.new(uri)
    when 'PUT'    then Net::HTTP::Put.new(uri)
    when 'DELETE' then Net::HTTP::Delete.new(uri)
    else Net::HTTP::Get.new(uri)
    end

    if body
      request['Content-Type'] = 'application/json'
      request.body = JSON.generate(body)
    end

    resp = http.request(request)
    return nil if resp.code == '204'
    return nil if resp.body.nil? || resp.body.empty?

    begin
      JSON.parse(resp.body)
    rescue JSON::ParserError
      resp.body
    end
  end

  # In-memory backbone for local dev.
  @local_store = {
    'nosql' => {}, 'cache' => {}, 'queues' => {},
    'blobs' => {}, 'locks' => {}, 'next_id' => 0,
  }

  def self._call_local(method, path, body = nil)
    s = @local_store
    base_path, qs = path.split('?', 2)
    query = {}
    if qs
      qs.split('&').each do |pair|
        k, v = pair.split('=', 2)
        query[URI.decode_www_form_component(k)] = URI.decode_www_form_component(v || '')
      end
    end

    # NoSQL
    if base_path == 'write' && method == 'POST'
      col = (body || {})['collection'] || 'default'
      s['nosql'][col] ||= {}
      s['next_id'] += 1
      key = s['next_id'].to_s
      s['nosql'][col][key] = body
      return { 'key' => key }
    end
    if base_path == 'read' && method == 'GET'
      col = query['collection'] || 'default'
      return (s['nosql'][col] || {})[query['key'] || '']
    end
    if base_path == 'nosql/list' && method == 'GET'
      col = query['collection'] || 'default'
      docs = s['nosql'][col] || {}
      field = query['field']
      value = query['value']
      results = []
      docs.each_value do |doc|
        next if field && doc[field].to_s != value
        results << doc
      end
      return results
    end
    if base_path == 'nosql/drop' && method == 'POST'
      s['nosql'].delete(query['collection'] || 'default')
      return nil
    end

    # Cache
    if base_path == 'cache/set' && method == 'POST'
      s['cache'][(body || {})['key'] || ''] = (body || {})['value']
      return nil
    end
    if base_path == 'cache/get' && method == 'GET'
      return s['cache'][query['key'] || '']
    end
    if base_path == 'cache/del'
      s['cache'].delete(query['key'] || '')
      return nil
    end

    # Queue
    if base_path == 'queue/push' && method == 'POST'
      name = (body || {})['queue'] || ''
      s['queues'][name] ||= []
      s['queues'][name] << (body || {})['body']
      return nil
    end
    if base_path == 'queue/pop' && method == 'POST'
      name = (body || {})['queue'] || ''
      q = s['queues'][name] || []
      return nil if q.empty?
      return q.shift
    end

    # Blob
    if base_path == 'blob/put' && method == 'POST'
      s['blobs'][(body || {})['name'] || ''] = (body || {})['data']
      return nil
    end
    if base_path == 'blob/get' && method == 'GET'
      return s['blobs'][query['name'] || '']
    end

    # Secret
    return nil if base_path == 'secret/get' && method == 'GET'

    # Lock
    if base_path == 'lock/acquire' && method == 'POST'
      name = (body || {})['name'] || ''
      return nil if s['locks'].key?(name)
      s['next_id'] += 1
      token = "local-lock-#{s['next_id']}"
      s['locks'][name] = token
      return { 'token' => token }
    end
    if base_path == 'lock/release' && method == 'POST'
      s['locks'].delete((body || {})['name'] || '')
      return nil
    end

    # Vector
    return nil if base_path == 'vector/insert' && method == 'POST'
    return [] if base_path == 'vector/search' && method == 'POST'

    nil
  end

  # ---------------------------------------------------------------------------
  # Secret
  # ---------------------------------------------------------------------------

  module Secret
    def self.get(name)
      resp = Drift._call('GET', "secret/get?name=#{URI.encode_www_form_component(name)}")
      resp.is_a?(String) ? resp : (resp ? JSON.generate(resp) : '')
    end

    def self.set(name, value)
      Drift._call('POST', 'secret/set', { 'name' => name, 'value' => value })
    end

    def self.delete(name)
      Drift._call('DELETE', "secret/delete?name=#{URI.encode_www_form_component(name)}")
    end
  end

  # ---------------------------------------------------------------------------
  # Cache
  # ---------------------------------------------------------------------------

  module Cache
    def self.get(key)
      Drift._call('GET', "cache/get?key=#{URI.encode_www_form_component(key)}")
    end

    def self.set(key, value, ttl: 0)
      payload = { 'key' => key, 'value' => value }
      payload['ttl'] = ttl if ttl > 0
      Drift._call('POST', 'cache/set', payload)
    end

    def self.delete(key)
      Drift._call('DELETE', "cache/del?key=#{URI.encode_www_form_component(key)}")
    end
  end

  # ---------------------------------------------------------------------------
  # NoSQL
  # ---------------------------------------------------------------------------

  module Nosql
    def self.collection(name)
      Collection.new(name)
    end

    class Collection
      def initialize(name)
        @name = name
      end

      def insert(doc)
        payload = { 'collection' => @name }
        if doc.is_a?(Hash)
          payload.merge!(doc)
        else
          payload['data'] = doc
        end
        resp = Drift._call('POST', 'write', payload)
        resp.is_a?(Hash) ? (resp['key'] || '') : ''
      end

      def read(key)
        Drift._call('GET', "read?collection=#{URI.encode_www_form_component(@name)}&key=#{URI.encode_www_form_component(key)}")
      end

      def list(filter = nil)
        path = "nosql/list?collection=#{URI.encode_www_form_component(@name)}"
        if filter
          filter.each do |k, v|
            path += "&field=#{URI.encode_www_form_component(k)}&value=#{URI.encode_www_form_component(v)}"
          end
        end
        resp = Drift._call('GET', path)
        resp.is_a?(Array) ? resp : []
      end

      def drop
        Drift._call('POST', "nosql/drop?collection=#{URI.encode_www_form_component(@name)}")
      end
    end
  end

  # ---------------------------------------------------------------------------
  # Queue
  # ---------------------------------------------------------------------------

  def self.queue(name)
    QueueHandle.new(name)
  end

  class QueueHandle
    def initialize(name)
      @name = name
    end

    def push(body)
      Drift._call('POST', 'queue/push', { 'queue' => @name, 'body' => body })
    end

    def pop
      Drift._call('POST', 'queue/pop', { 'queue' => @name })
    end
  end

  # ---------------------------------------------------------------------------
  # Blob
  # ---------------------------------------------------------------------------

  module Blob
    def self.put(name, data)
      Drift._call('POST', 'blob/put', { 'name' => name, 'data' => data })
    end

    def self.get(name)
      Drift._call('GET', "blob/get?name=#{URI.encode_www_form_component(name)}")
    end
  end

  # ---------------------------------------------------------------------------
  # Lock
  # ---------------------------------------------------------------------------

  module Lock
    def self.acquire(name, ttl: 30)
      resp = Drift._call('POST', 'lock/acquire', { 'name' => name, 'ttl' => ttl })
      (resp || {})['token'] || ''
    end

    def self.release(name, token)
      Drift._call('POST', 'lock/release', { 'name' => name, 'token' => token })
    end
  end

  # ---------------------------------------------------------------------------
  # Vector
  # ---------------------------------------------------------------------------

  module Vector
    def self.collection(name)
      VectorCollection.new(name)
    end

    class VectorCollection
      def initialize(name)
        @name = name
      end

      def insert(id, vector, metadata = nil)
        Drift._call('POST', 'vector/insert', {
          'collection' => @name, 'id' => id,
          'vector' => vector, 'metadata' => metadata,
        })
      end

      def search(vector, k: 10)
        resp = Drift._call('POST', 'vector/search', {
          'collection' => @name, 'vector' => vector, 'k' => k,
        })
        resp.is_a?(Array) ? resp : []
      end
    end
  end

  # ---------------------------------------------------------------------------
  # Logging
  # ---------------------------------------------------------------------------

  def self.log(msg)
    $stderr.puts msg.to_s
    $stderr.flush
  end

  # ---------------------------------------------------------------------------
  # HTTP client
  # ---------------------------------------------------------------------------

  def self.http_request(method, url, headers = {}, body = nil)
    uri = URI(url)
    http = Net::HTTP.new(uri.host, uri.port)
    http.use_ssl = uri.scheme == 'https'

    request = case method.upcase
    when 'GET'    then Net::HTTP::Get.new(uri)
    when 'POST'   then Net::HTTP::Post.new(uri)
    when 'PUT'    then Net::HTTP::Put.new(uri)
    when 'DELETE' then Net::HTTP::Delete.new(uri)
    when 'PATCH'  then Net::HTTP::Patch.new(uri)
    else Net::HTTP::Get.new(uri)
    end

    (headers || {}).each { |k, v| request[k] = v }
    request.body = body if body

    resp = http.request(request)
    { 'status' => resp.code.to_i, 'body' => resp.body }
  end

end
