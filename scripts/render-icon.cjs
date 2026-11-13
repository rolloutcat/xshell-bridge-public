'use strict';

const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

const assets = path.resolve(__dirname, '..', 'assets');
const sizes = [16, 24, 32, 48, 64, 128, 256];

function edgePath() {
  if (process.env.BROWSER_PATH) return process.env.BROWSER_PATH;
  const candidates = [
    process.env['ProgramFiles(x86)'],
    process.env.ProgramFiles,
    process.env.LOCALAPPDATA
  ].filter(Boolean).map(root => path.join(root, 'Microsoft', 'Edge', 'Application', 'msedge.exe'));
  const found = candidates.find(candidate => fs.existsSync(candidate));
  if (!found) throw new Error('Microsoft Edge not found; set BROWSER_PATH to msedge.exe.');
  return found;
}

async function main() {
  const svg = fs.readFileSync(path.join(assets, 'app-icon.svg'), 'utf8');
  let browser;
  try {
    browser = await chromium.launch({ executablePath: edgePath(), headless: true });
    const page = await browser.newPage({
      viewport: { width: 256, height: 256 },
      deviceScaleFactor: 1
    });
    // The trusted local SVG needs no network or external resources.
    await page.route('**/*', route => route.abort());
    await page.setContent('<!doctype html><html><head><style>html,body{margin:0;padding:0;background:transparent}svg{display:block}</style></head><body>' + svg + '</body></html>');
    const icon = page.locator('svg');
    const images = [];
    for (const size of sizes) {
      await icon.evaluate((element, value) => {
        element.setAttribute('width', String(value));
        element.setAttribute('height', String(value));
      }, size);
      const png = await icon.screenshot({ type: 'png', omitBackground: true });
      if (png.readUInt32BE(16) !== size || png.readUInt32BE(20) !== size) {
        throw new Error('Unexpected PNG dimensions for ' + size);
      }
      images.push(png);
    }

    // ICONDIR followed by ICONDIRENTRY records and Vista+ PNG payloads.
    const directory = Buffer.alloc(6 + 16 * sizes.length);
    directory.writeUInt16LE(0, 0);
    directory.writeUInt16LE(1, 2);
    directory.writeUInt16LE(sizes.length, 4);
    let offset = directory.length;
    sizes.forEach((size, index) => {
      const entry = 6 + index * 16;
      directory.writeUInt8(size === 256 ? 0 : size, entry);
      directory.writeUInt8(size === 256 ? 0 : size, entry + 1);
      directory.writeUInt8(0, entry + 2);
      directory.writeUInt8(0, entry + 3);
      directory.writeUInt16LE(1, entry + 4);
      directory.writeUInt16LE(32, entry + 6);
      directory.writeUInt32LE(images[index].length, entry + 8);
      directory.writeUInt32LE(offset, entry + 12);
      offset += images[index].length;
    });
    fs.writeFileSync(path.join(assets, 'app-icon.ico'), Buffer.concat([directory, ...images]));
    fs.writeFileSync(path.join(assets, 'app-icon.png'), images[images.length - 1]);
  } finally {
    if (browser) await browser.close();
  }
}

main().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
