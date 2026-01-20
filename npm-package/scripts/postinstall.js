#!/usr/bin/env node

const https = require('https');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { execSync } = require('child_process');

const packageJson = require('../package.json');
const VERSION = packageJson.version;

function getPlatformInfo() {
  const platform = os.platform();
  const arch = os.arch();

  let platformName;
  let archName;
  let binaryName = 'bdc';

  switch (platform) {
    case 'darwin':
      platformName = 'darwin';
      break;
    case 'linux':
      platformName = 'linux';
      break;
    case 'win32':
      platformName = 'windows';
      binaryName = 'bdc.exe';
      break;
    default:
      throw new Error(`Unsupported platform: ${platform}`);
  }

  switch (arch) {
    case 'x64':
      archName = 'amd64';
      break;
    case 'arm64':
      archName = 'arm64';
      break;
    default:
      throw new Error(`Unsupported architecture: ${arch}`);
  }

  return { platformName, archName, binaryName };
}

function downloadFile(url, dest) {
  return new Promise((resolve, reject) => {
    console.log(`Downloading from: ${url}`);
    const file = fs.createWriteStream(dest);

    const request = https.get(url, (response) => {
      if (response.statusCode === 301 || response.statusCode === 302) {
        const redirectUrl = response.headers.location;
        console.log(`Following redirect to: ${redirectUrl}`);
        downloadFile(redirectUrl, dest).then(resolve).catch(reject);
        return;
      }

      if (response.statusCode !== 200) {
        reject(new Error(`Failed to download: HTTP ${response.statusCode}`));
        return;
      }

      response.pipe(file);

      file.on('finish', () => {
        file.close((err) => {
          if (err) reject(err);
          else resolve();
        });
      });
    });

    request.on('error', (err) => {
      fs.unlink(dest, () => {});
      reject(err);
    });

    file.on('error', (err) => {
      fs.unlink(dest, () => {});
      reject(err);
    });
  });
}

function extractTarGz(tarGzPath, destDir, binaryName) {
  console.log(`Extracting ${tarGzPath}...`);

  try {
    execSync(`tar -xzf "${tarGzPath}" -C "${destDir}"`, { stdio: 'inherit' });

    const extractedBinary = path.join(destDir, binaryName);

    if (!fs.existsSync(extractedBinary)) {
      throw new Error(`Binary not found after extraction: ${extractedBinary}`);
    }

    if (os.platform() !== 'win32') {
      fs.chmodSync(extractedBinary, 0o755);
    }

    console.log(`Binary extracted to: ${extractedBinary}`);
  } catch (err) {
    throw new Error(`Failed to extract archive: ${err.message}`);
  }
}

async function extractZip(zipPath, destDir, binaryName) {
  console.log(`Extracting ${zipPath}...`);

  try {
    if (os.platform() === 'win32') {
      execSync(`powershell -command "Expand-Archive -Path '${zipPath}' -DestinationPath '${destDir}' -Force"`, { stdio: 'inherit' });
    } else {
      execSync(`unzip -o "${zipPath}" -d "${destDir}"`, { stdio: 'inherit' });
    }

    const extractedBinary = path.join(destDir, binaryName);

    if (!fs.existsSync(extractedBinary)) {
      throw new Error(`Binary not found after extraction: ${extractedBinary}`);
    }

    console.log(`Binary extracted to: ${extractedBinary}`);
  } catch (err) {
    throw new Error(`Failed to extract archive: ${err.message}`);
  }
}

async function installFromGo() {
  console.log('Attempting to install via go install...');

  try {
    execSync('go install github.com/brianevanmiller/beadcrumbs/cmd/bdc@latest', {
      stdio: 'inherit',
      env: { ...process.env }
    });

    // Find where Go installed it
    const gopath = execSync('go env GOPATH', { encoding: 'utf8' }).trim();
    const goBinaryPath = path.join(gopath, 'bin', os.platform() === 'win32' ? 'bdc.exe' : 'bdc');

    if (fs.existsSync(goBinaryPath)) {
      // Copy to our bin directory
      const binDir = path.join(__dirname, '..', 'bin');
      const binaryName = os.platform() === 'win32' ? 'bdc.exe' : 'bdc';
      const destPath = path.join(binDir, binaryName);

      fs.copyFileSync(goBinaryPath, destPath);
      if (os.platform() !== 'win32') {
        fs.chmodSync(destPath, 0o755);
      }

      console.log(`bdc installed successfully via go install`);
      return true;
    }
  } catch (err) {
    console.log(`go install failed: ${err.message}`);
  }

  return false;
}

async function install() {
  try {
    const { platformName, archName, binaryName } = getPlatformInfo();

    console.log(`Installing bdc v${VERSION} for ${platformName}-${archName}...`);

    const binDir = path.join(__dirname, '..', 'bin');
    const binaryPath = path.join(binDir, binaryName);

    if (!fs.existsSync(binDir)) {
      fs.mkdirSync(binDir, { recursive: true });
    }

    // Try downloading from GitHub releases first
    const releaseVersion = VERSION;
    const archiveExt = platformName === 'windows' ? 'zip' : 'tar.gz';
    const archiveName = `beadcrumbs_${releaseVersion}_${platformName}_${archName}.${archiveExt}`;
    const downloadUrl = `https://github.com/brianevanmiller/beadcrumbs/releases/download/v${releaseVersion}/${archiveName}`;
    const archivePath = path.join(binDir, archiveName);

    try {
      console.log(`Downloading bdc binary...`);
      await downloadFile(downloadUrl, archivePath);

      if (platformName === 'windows') {
        await extractZip(archivePath, binDir, binaryName);
      } else {
        extractTarGz(archivePath, binDir, binaryName);
      }

      fs.unlinkSync(archivePath);

      const output = execSync(`"${binaryPath}" --help`, { encoding: 'utf8' });
      console.log(`bdc installed successfully`);
      return;

    } catch (downloadErr) {
      console.log(`GitHub release download failed: ${downloadErr.message}`);
      console.log('Falling back to go install...');
    }

    // Try go install as fallback
    if (await installFromGo()) {
      return;
    }

    throw new Error('All installation methods failed');

  } catch (err) {
    console.error(`Error installing bdc: ${err.message}`);
    console.error('');
    console.error('Installation failed. You can try:');
    console.error('1. Installing Go and running: go install github.com/brianevanmiller/beadcrumbs/cmd/bdc@latest');
    console.error('2. Building from source: git clone https://github.com/brianevanmiller/beadcrumbs.git && cd beadcrumbs && go build -o bdc ./cmd/bdc/');
    console.error('3. Opening an issue: https://github.com/brianevanmiller/beadcrumbs/issues');
    process.exit(1);
  }
}

if (!process.env.CI) {
  install();
} else {
  console.log('Skipping binary download in CI environment');
}
