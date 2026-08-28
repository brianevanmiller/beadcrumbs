#!/usr/bin/env node
//
// Fetch the prebuilt bdc release archive for this platform, verify its SHA-256
// against the release's checksums.txt, and unpack one binary into bin/.
//
// There is no source-build fallback. bdc needs CGO, ICU4C headers, and Go
// >= 1.26.2; `go install` from a postinstall hook would either fail slowly or
// produce a binary linked against a Homebrew ICU path that does not exist on
// the next machine. Failing loudly with instructions is the honest outcome.
//
// BDC_BASE_URL overrides the download base. It accepts an https:// URL or a
// local directory (with or without a file:// scheme), which is how
// test/packaging/smoke.sh exercises this file against a locally built asset
// with no network and no publish.

const https = require('https');
const fs = require('fs');
const path = require('path');
const os = require('os');
const crypto = require('crypto');
const { execFileSync } = require('child_process');

const VERSION = require('../package.json').version;
const DEFAULT_BASE = `https://github.com/brianevanmiller/beadcrumbs/releases/download/v${VERSION}`;

const MANUAL = [
  '',
  'bdc could not be installed automatically.',
  '',
  'Prebuilt binaries exist for macOS and Linux on arm64 and amd64 only.',
  'Windows is not supported.',
  '',
  'To build from source you need Go >= 1.26.2, CGO, and ICU4C:',
  '  macOS:  brew install icu4c',
  '          CGO_CPPFLAGS="-I$(brew --prefix icu4c)/include" \\',
  '          CGO_LDFLAGS="-L$(brew --prefix icu4c)/lib" \\',
  `          go install github.com/brianevanmiller/beadcrumbs/cmd/bdc@v${VERSION}`,
  '  Debian: sudo apt install libicu-dev',
  `          go install github.com/brianevanmiller/beadcrumbs/cmd/bdc@v${VERSION}`,
  '',
  'Releases:  https://github.com/brianevanmiller/beadcrumbs/releases',
  'Issues:    https://github.com/brianevanmiller/beadcrumbs/issues',
  '',
].join('\n');

function target() {
  const platform = os.platform();
  const arch = os.arch();
  if (platform === 'win32') {
    throw new Error('Windows is not supported. bdc runs on macOS and Linux only.');
  }
  if (platform !== 'darwin' && platform !== 'linux') {
    throw new Error(`Unsupported platform: ${platform}. bdc runs on macOS and Linux only.`);
  }
  const archName = { x64: 'amd64', arm64: 'arm64' }[arch];
  if (!archName) {
    throw new Error(`Unsupported architecture: ${arch}. bdc ships arm64 and amd64 only.`);
  }
  return { platform, archName };
}

function localBase(base) {
  if (base.startsWith('file://')) return base.slice('file://'.length);
  if (base.startsWith('https://') || base.startsWith('http://')) return null;
  return base;
}

function httpGet(url, dest, redirects = 0) {
  return new Promise((resolve, reject) => {
    if (redirects > 5) return reject(new Error(`too many redirects for ${url}`));
    const file = fs.createWriteStream(dest);
    const fail = (err) => { file.destroy(); fs.rm(dest, { force: true }, () => reject(err)); };
    https.get(url, (res) => {
      if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        res.resume();
        file.destroy();
        fs.rm(dest, { force: true }, () =>
          httpGet(res.headers.location, dest, redirects + 1).then(resolve, reject));
        return;
      }
      if (res.statusCode !== 200) {
        res.resume();
        return fail(new Error(`GET ${url} returned HTTP ${res.statusCode}`));
      }
      res.pipe(file);
      file.on('finish', () => file.close((err) => (err ? fail(err) : resolve())));
    }).on('error', fail);
    file.on('error', fail);
  });
}

async function fetch(base, name, dest) {
  const dir = localBase(base);
  if (dir) {
    const src = path.join(dir, name);
    if (!fs.existsSync(src)) throw new Error(`asset not found: ${src}`);
    fs.copyFileSync(src, dest);
    return;
  }
  await httpGet(`${base}/${name}`, dest);
}

function sha256(file) {
  return crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
}

// The checksum file is the release's own manifest, so a truncated or swapped
// archive is caught before anything is unpacked or made executable.
function verify(checksumFile, archiveFile, archiveName) {
  const line = fs.readFileSync(checksumFile, 'utf8')
    .split('\n')
    .map((l) => l.trim())
    .find((l) => l.endsWith(archiveName) || l.endsWith(`*${archiveName}`));
  if (!line) throw new Error(`${archiveName} is not listed in checksums.txt`);
  const expected = line.split(/\s+/)[0].toLowerCase();
  const actual = sha256(archiveFile);
  if (expected !== actual) {
    throw new Error(`checksum mismatch for ${archiveName}: expected ${expected}, got ${actual}`);
  }
}

async function install() {
  const { platform, archName } = target();
  const base = process.env.BDC_BASE_URL || DEFAULT_BASE;
  const archiveName = `beadcrumbs_${VERSION}_${platform}_${archName}.tar.gz`;

  const binDir = path.join(__dirname, '..', 'bin');
  fs.mkdirSync(binDir, { recursive: true });
  const work = fs.mkdtempSync(path.join(os.tmpdir(), 'bdc-install-'));

  try {
    console.log(`Installing bdc v${VERSION} for ${platform}/${archName} from ${base}`);
    const archive = path.join(work, archiveName);
    const checksums = path.join(work, 'checksums.txt');
    await fetch(base, archiveName, archive);
    await fetch(base, 'checksums.txt', checksums);

    verify(checksums, archive, archiveName);
    console.log('Checksum verified.');

    execFileSync('tar', ['-xzf', archive, '-C', work, 'bdc'], { stdio: 'inherit' });
    const binary = path.join(binDir, 'bdc');
    fs.copyFileSync(path.join(work, 'bdc'), binary);
    fs.chmodSync(binary, 0o755);

    execFileSync(binary, ['version'], { stdio: 'inherit' });
  } finally {
    fs.rmSync(work, { recursive: true, force: true });
  }
}

install().catch((err) => {
  console.error(`Error installing bdc: ${err.message}`);
  console.error(MANUAL);
  process.exit(1);
});
