#!/usr/bin/env node
/*
 * Removes stray Go source files that some npm packages ship inside their
 * published tarballs (e.g. flatted/golang/**\/*.go, pulled in transitively by
 * eslint -> file-entry-cache -> flat-cache -> flatted). The JS build never uses
 * them, but because ui/ lives under this repo's Go module, `go build|vet|list
 * ./...` and golangci-lint would otherwise scan them as if they were ours.
 *
 * Runs as a `postinstall` hook so the ui/ tree stays free of any .go files
 * after every install. cwd is the package dir, so it prunes that package's own
 * node_modules.
 */
const fs = require('fs');
const path = require('path');

const nodeModules = path.join(process.cwd(), 'node_modules');
let removed = 0;

function walk(dir) {
  let entries;
  try {
    entries = fs.readdirSync(dir, { withFileTypes: true });
  } catch {
    return;
  }
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      walk(full);
    } else if (entry.isFile() && entry.name.endsWith('.go')) {
      try {
        fs.rmSync(full);
        removed++;
      } catch {
        /* best-effort */
      }
    }
  }
}

if (fs.existsSync(nodeModules)) {
  walk(nodeModules);
  if (removed > 0) {
    console.log(`prune-stray-go: removed ${removed} stray .go file(s) from node_modules`);
  }
}
