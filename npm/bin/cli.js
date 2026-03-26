#!/usr/bin/env node
const { execFileSync } = require("child_process");
const path = require("path");

const binaryName =
  process.platform === "win32" ? "local-docs-mcp.exe" : "local-docs-mcp";
const binaryPath = path.join(__dirname, binaryName);

try {
  execFileSync(binaryPath, process.argv.slice(2), { stdio: "inherit" });
} catch (e) {
  if (e.status !== null) {
    process.exitCode = e.status;
  } else {
    throw e;
  }
}
