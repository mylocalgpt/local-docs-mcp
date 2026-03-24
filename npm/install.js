const fs = require('fs');
const path = require('path');
const https = require('https');
const crypto = require('crypto');
const { execSync } = require('child_process');

const platform = process.platform;
const arch = process.arch;

const archMap = { x64: 'amd64', arm64: 'arm64' };
const platformMap = { darwin: 'darwin', linux: 'linux', win32: 'windows' };

const mappedPlatform = platformMap[platform];
const mappedArch = archMap[arch];

if (!mappedPlatform || !mappedArch) {
  console.error(`Unsupported platform: ${platform} ${arch}. Supported: darwin/x64, darwin/arm64, linux/x64, linux/arm64, win32/x64`);
  process.exit(1);
}

if (platform === 'win32' && arch === 'arm64') {
  console.error(`Unsupported platform: ${platform} ${arch}. Supported: darwin/x64, darwin/arm64, linux/x64, linux/arm64, win32/x64`);
  process.exit(1);
}

const packageJson = require('./package.json');
const version = packageJson.version;

const archiveExt = platform === 'win32' ? 'zip' : 'tar.gz';
const archiveName = `local-docs-mcp_${mappedPlatform}_${mappedArch}.${archiveExt}`;
const binDir = path.join(__dirname, 'bin');
const binaryName = platform === 'win32' ? 'local-docs-mcp.exe' : 'local-docs-mcp';
const binaryPath = path.join(binDir, binaryName);

const baseUrl = `https://github.com/mylocalgpt/local-docs-mcp/releases/download/v${version}`;
const checksumsUrl = `${baseUrl}/checksums.txt`;
const archiveUrl = `${baseUrl}/${archiveName}`;

function download(url, cb) {
  https.get(url, (res) => {
    if (res.statusCode === 301 || res.statusCode === 302) {
      return download(res.headers.location, cb);
    }
    if (res.statusCode !== 200) {
      cb(new Error(`HTTP ${res.statusCode}`));
      return;
    }
    const chunks = [];
    res.on('data', (chunk) => chunks.push(chunk));
    res.on('end', () => cb(null, Buffer.concat(chunks)));
  }).on('error', cb);
}

function downloadFile(url) {
  return new Promise((resolve, reject) => {
    download(url, (err, data) => {
      if (err) reject(err);
      else resolve(data);
    });
  });
}

async function main() {
  console.log(`Downloading local-docs-mcp v${version} for ${platform}/${arch}...`);

  let checksums;
  try {
    checksums = await downloadFile(checksumsUrl);
    checksums = checksums.toString('utf8');
  } catch (err) {
    console.error(`Failed to download checksums: ${err.message}. Check if release v${version} exists.`);
    process.exit(1);
  }

  const archiveBuffer = await downloadFile(archiveUrl).catch((err) => {
    console.error(`Failed to download binary: ${archiveUrl}. Check if release v${version} exists.`);
    process.exit(1);
  });

  const expectedChecksum = checksums.split('\n').find((line) => line.includes(archiveName));
  if (!expectedChecksum) {
    console.error(`Checksum not found for ${archiveName}`);
    process.exit(1);
  }

  const expectedHash = expectedChecksum.split(/\s+/)[0];
  const actualHash = crypto.createHash('sha256').update(archiveBuffer).digest('hex');

  if (actualHash !== expectedHash) {
    console.error('Checksum verification failed. The downloaded archive may be corrupted or tampered with.');
    process.exit(1);
  }

  const archivePath = path.join(binDir, archiveName);
  fs.writeFileSync(archivePath, archiveBuffer);

  try {
    if (platform === 'win32') {
      execSync(`tar -xf "${archivePath}" -C "${binDir}"`, { stdio: 'inherit' });
    } else {
      execSync(`tar xzf "${archivePath}" -C "${binDir}"`, { stdio: 'inherit' });
    }
  } catch (err) {
    console.error(`Failed to extract archive: ${err.message}`);
    process.exit(1);
  }

  fs.unlinkSync(archivePath);

  if (platform === 'win32') {
    const extractedBinary = path.join(binDir, 'local-docs-mcp.exe');
    if (fs.existsSync(extractedBinary) && extractedBinary !== binaryPath) {
      fs.renameSync(extractedBinary, binaryPath);
    }
  } else {
    fs.chmodSync(binaryPath, 0o755);
  }

  console.log('local-docs-mcp installed successfully.');
}

main().catch((err) => {
  console.error(`Installation failed: ${err.message}`);
  process.exit(1);
});
