const http = require('http');
const fs = require('fs');
const path = require('path');

const HOST = process.env.WEB_ROBOT_HOST || '127.0.0.1';
const PORT = Number(process.env.WEB_ROBOT_PORT || 9021);
const API_TARGET = process.env.WEB_ROBOT_API_TARGET || 'http://127.0.0.1:9010';
const ROOT = __dirname;

function shouldProxy(pathname) {
  if (pathname === '/healthz') return true;
  if (pathname.startsWith('/v1/')) return true;
  return false;
}

const MIME_BY_EXT = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'application/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml'
};

function sendJSON(res, code, payload) {
  res.statusCode = code;
  res.setHeader('content-type', 'application/json; charset=utf-8');
  res.end(JSON.stringify(payload));
}

function safeJoin(root, targetPath) {
  const clean = path.normalize(targetPath).replace(/^([.][.][\\/])+/, '');
  return path.join(root, clean);
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    req.on('data', (c) => chunks.push(c));
    req.on('end', () => resolve(Buffer.concat(chunks)));
    req.on('error', reject);
  });
}

async function proxyToAPI(req, res, pathname) {
  const body = await readBody(req);
  const url = API_TARGET + pathname + (req.url.includes('?') ? req.url.slice(req.url.indexOf('?')) : '');

  const headers = {};
  Object.keys(req.headers || {}).forEach((k) => {
    if (k.toLowerCase() === 'host' || k.toLowerCase() === 'content-length' || k.toLowerCase() === 'connection') return;
    headers[k] = req.headers[k];
  });

  const method = req.method || 'GET';
  const init = { method, headers };
  if (method !== 'GET' && method !== 'HEAD' && body.length > 0) {
    init.body = body;
  }

  const response = await fetch(url, init);
  const buf = Buffer.from(await response.arrayBuffer());

  res.statusCode = response.status;
  response.headers.forEach((value, key) => {
    if (key.toLowerCase() === 'transfer-encoding' || key.toLowerCase() === 'content-length' || key.toLowerCase() === 'connection') return;
    res.setHeader(key, value);
  });
  res.end(buf);
}

function serveStatic(res, pathname) {
  let filePath = pathname === '/' ? '/web_robot_body.html' : pathname;
  if (filePath.includes('..')) {
    sendJSON(res, 400, { error: 'invalid path' });
    return;
  }

  const abs = safeJoin(ROOT, filePath);
  if (!abs.startsWith(ROOT)) {
    sendJSON(res, 403, { error: 'forbidden' });
    return;
  }

  fs.stat(abs, (err, st) => {
    if (err || !st.isFile()) {
      sendJSON(res, 404, { error: 'not found' });
      return;
    }

    const ext = path.extname(abs).toLowerCase();
    res.statusCode = 200;
    res.setHeader('content-type', MIME_BY_EXT[ext] || 'application/octet-stream');
    const stream = fs.createReadStream(abs);
    stream.on('error', () => sendJSON(res, 500, { error: 'read failed' }));
    stream.pipe(res);
  });
}

const server = http.createServer(async (req, res) => {
  try {
    const parsed = new URL(req.url || '/', 'http://local');
    const pathname = parsed.pathname;

    if ((req.method || 'GET').toUpperCase() === 'OPTIONS') {
      res.statusCode = 204;
      res.end();
      return;
    }

    if (shouldProxy(pathname)) {
      await proxyToAPI(req, res, pathname);
      return;
    }

    serveStatic(res, pathname);
  } catch (err) {
    sendJSON(res, 500, { error: err && err.message ? err.message : String(err) });
  }
});

server.listen(PORT, HOST, () => {
  console.log(`[web_robot_dev_server] listening on http://${HOST}:${PORT}`);
  console.log(`[web_robot_dev_server] proxy target: ${API_TARGET}`);
});
