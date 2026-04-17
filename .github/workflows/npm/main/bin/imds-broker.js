#!/usr/bin/env node
'use strict';
const { execFileSync } = require('child_process');
const path = require('path');

const platforms = {
  'linux-x64':    '@jamestelfer/imds-broker-linux-x64',
  'linux-arm64':  '@jamestelfer/imds-broker-linux-arm64',
  'darwin-x64':   '@jamestelfer/imds-broker-darwin-x64',
  'darwin-arm64': '@jamestelfer/imds-broker-darwin-arm64',
  'win32-x64':    '@jamestelfer/imds-broker-windows-x64',
  'win32-arm64':  '@jamestelfer/imds-broker-windows-arm64',
};

const key = `${process.platform}-${process.arch}`;
const pkg = platforms[key];
if (!pkg) {
  console.error(`imds-broker: unsupported platform ${key}`);
  process.exit(1);
}

const bin = process.platform === 'win32' ? 'imds-broker.exe' : 'imds-broker';
const binaryPath = path.join(
  path.dirname(require.resolve(`${pkg}/package.json`)),
  bin
);

execFileSync(binaryPath, process.argv.slice(2), { stdio: 'inherit' });
