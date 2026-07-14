#!/usr/bin/env node
// Local stand-in for Cloudflare Pages `_redirects` (Hugo server does not
// apply them). Sits in front of `hugo server` and rewrites dynamic detail
// routes to the pre-built shell pages React hydrates from.
//
// Usage: HUGO_ORIGIN=http://127.0.0.1:5171 node local-redirects.mjs
// Listens on LISTEN_PORT (default 5170).

import http from 'node:http';
import { URL } from 'node:url';

const LISTEN = Number(process.env.LISTEN_PORT || 5170);
const HUGO = process.env.HUGO_ORIGIN || 'http://127.0.0.1:5171';

// Mirrors ui/static/_redirects. First match wins. Splat (*) captures the rest.
const RULES = [
  { from: /^\/jobs\/$/, to: '/jobs/' },
  { from: /^\/jobs\/.+/, to: '/job/' },
  { from: /^\/scholarships\/$/, to: '/scholarships/' },
  { from: /^\/scholarships\/.+/, to: '/scholarship/' },
  { from: /^\/tenders\/$/, to: '/tenders/' },
  { from: /^\/tenders\/.+/, to: '/tender/' },
  { from: /^\/deals\/$/, to: '/deals/' },
  { from: /^\/deals\/.+/, to: '/deal/' },
  { from: /^\/funding\/$/, to: '/funding/' },
  { from: /^\/funding\/.+/, to: '/funding-detail/' },
  { from: /^\/categories\/$/, to: '/categories/' },
  { from: /^\/categories\/.+/, to: '/categories/' },
];

function rewrite(pathname) {
  for (const r of RULES) {
    if (r.from.test(pathname)) return r.to;
  }
  return pathname;
}

const server = http.createServer((req, res) => {
  const incoming = new URL(req.url || '/', `http://${req.headers.host}`);
  const targetPath = rewrite(incoming.pathname);
  const target = new URL(targetPath + incoming.search, HUGO);

  const headers = { ...req.headers, host: new URL(HUGO).host };
  const proxy = http.request(
    target,
    { method: req.method, headers },
    (upstream) => {
      res.writeHead(upstream.statusCode || 502, upstream.headers);
      upstream.pipe(res);
    }
  );
  proxy.on('error', (err) => {
    res.writeHead(502, { 'content-type': 'text/plain' });
    res.end(`local-redirects proxy error: ${err.message}`);
  });
  req.pipe(proxy);
});

server.listen(LISTEN, '0.0.0.0', () => {
  console.log(`[local-redirects] http://0.0.0.0:${LISTEN} → ${HUGO}`);
});
