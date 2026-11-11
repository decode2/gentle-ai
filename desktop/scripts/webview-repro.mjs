// Reproduce the WebView2 environment: serve dist, open in chromium, capture console errors
import { createRequire } from 'module';
const require2 = createRequire('/home/linuxbrew/.linuxbrew/lib/node_modules/playwright/');
const { chromium } = require2('playwright');
import http from 'http';
import fs from 'fs';
import path from 'path';

const dist = 'dist';
const server = http.createServer((req, res) => {
  let p = req.url.split('?')[0];
  if (p === '/') p = '/index.html';
  const file = path.join(dist, p);
  if (fs.existsSync(file)) {
    const ext = path.extname(file);
    const types = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css' };
    res.setHeader('Content-Type', types[ext] ?? 'application/octet-stream');
    res.end(fs.readFileSync(file));
  } else {
    res.statusCode = 404;
    res.end('nope: ' + p);
  }
});
await new Promise(r => server.listen(18199, r));

const browser = await chromium.launch();
const page = await browser.newPage();
const errors = [];
page.on('console', m => { if (m.type() === 'error') errors.push('[console] ' + m.text()); });
page.on('pageerror', e => errors.push('[pageerror] ' + e.message));
page.on('requestfailed', r => errors.push('[reqfail] ' + r.url() + ' — ' + r.failure()?.errorText));

await page.goto('http://127.0.0.1:18199/', { waitUntil: 'networkidle', timeout: 20000 }).catch(e => errors.push('[goto] ' + e.message));
await page.waitForTimeout(1500);
const bodyText = await page.evaluate(() => document.body.innerText.slice(0, 300));
const rootHTML = await page.evaluate(() => document.getElementById('root')?.innerHTML.length ?? -1);
console.log('ROOT innerHTML length:', rootHTML);
console.log('BODY text:', JSON.stringify(bodyText));
console.log('ERRORS:', errors.length ? errors : 'none');
await browser.close();
server.close();
