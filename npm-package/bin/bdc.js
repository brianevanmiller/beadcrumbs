#!/usr/bin/env node

const { spawn } = require('child_process');
const path = require('path');
const os = require('os');

// Determine binary name based on platform
const binaryName = os.platform() === 'win32' ? 'bdc.exe' : 'bdc';
const binaryPath = path.join(__dirname, binaryName);

// Spawn the binary with all arguments
const child = spawn(binaryPath, process.argv.slice(2), {
  stdio: 'inherit',
  windowsHide: true
});

child.on('error', (err) => {
  if (err.code === 'ENOENT') {
    console.error('Error: bdc binary not found. Try reinstalling:');
    console.error('  npm uninstall -g @beadcrumbs/bdc');
    console.error('  npm install -g @beadcrumbs/bdc');
    process.exit(1);
  }
  console.error('Error executing bdc:', err.message);
  process.exit(1);
});

child.on('exit', (code) => {
  process.exit(code || 0);
});
