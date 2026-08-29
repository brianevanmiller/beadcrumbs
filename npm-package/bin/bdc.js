#!/usr/bin/env node

const { spawn } = require('child_process');
const path = require('path');

const binaryPath = path.join(__dirname, 'bdc');

const child = spawn(binaryPath, process.argv.slice(2), { stdio: 'inherit' });

child.on('error', (err) => {
  if (err.code === 'ENOENT') {
    console.error('Error: the bdc binary is missing. The postinstall step did not complete.');
    console.error('  npm uninstall -g @beadcrumbs/bdc && npm install -g @beadcrumbs/bdc');
    process.exit(1);
  }
  console.error(`Error executing bdc: ${err.message}`);
  process.exit(1);
});

// bdc's exit codes are contractual (0-8), so pass the child's status through
// unchanged. A signal death becomes 128+signo rather than a fabricated 0.
child.on('exit', (code, signal) => {
  process.exit(signal ? 128 + require('os').constants.signals[signal] : code);
});
