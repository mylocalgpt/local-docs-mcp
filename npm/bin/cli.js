#!/usr/bin/env node
const { execFileSync } = require("child_process");
const os = require("os");

const platformPackages = {
  "darwin x64": "@local-docs-mcp/darwin-x64",
  "darwin arm64": "@local-docs-mcp/darwin-arm64",
  "linux x64": "@local-docs-mcp/linux-x64",
  "linux arm64": "@local-docs-mcp/linux-arm64",
  "win32 x64": "@local-docs-mcp/win32-x64",
};

const key = `${process.platform} ${os.arch()}`;
const pkg = platformPackages[key];
if (!pkg) {
  console.error(`Unsupported platform: ${key}. Use 'go install' instead.`);
  process.exit(1);
}

const binaryName = process.platform === "win32" ? "local-docs-mcp.exe" : "local-docs-mcp";

let binPath;
try {
  const pkgDir = require.resolve(`${pkg}/package.json`);
  binPath = require("path").join(require("path").dirname(pkgDir), binaryName);
} catch {
  console.error(
    `Platform package ${pkg} not found. This can happen with --no-optional.\n` +
    `Install manually: go install github.com/mylocalgpt/local-docs-mcp/cmd/local-docs-mcp@latest`
  );
  process.exit(1);
}

try {
  execFileSync(binPath, process.argv.slice(2), { stdio: "inherit" });
} catch (e) {
  if (e.status !== null) process.exitCode = e.status;
  else throw e;
}
