// Two browsers sign up on a Sharkord server, join the same voice channel
// with Chromium's fake microphone, and pass when each one's inbound audio
// keeps growing.
//
//   node voice.mjs https://chat-a.example.com [--insecure]
import { chromium } from 'playwright';

const url = process.argv[2];
const insecure = process.argv.includes('--insecure');
if (!url) {
  console.error('usage: node voice.mjs <server url> [--insecure]');
  process.exit(2);
}

const browser = await chromium.launch({
  args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream']
});

// Keep every RTCPeerConnection the page makes, to read its stats.
const trackConnections = () => {
  const Original = window.RTCPeerConnection;
  window.__pcs = [];
  window.RTCPeerConnection = function (...args) {
    const pc = new Original(...args);
    window.__pcs.push(pc);
    return pc;
  };
  window.RTCPeerConnection.prototype = Original.prototype;
  Object.setPrototypeOf(window.RTCPeerConnection, Original);
};

const expectShiver = process.argv.includes('--expect-shiver');
const shiverBundles = new Map();

const join = async (name) => {
  const context = await browser.newContext({ ignoreHTTPSErrors: insecure, permissions: ['microphone'] });
  const page = await context.newPage();
  page.on('response', (r) => {
    if (r.url().includes('/plugin-bundle/shiver/client/index.js')) shiverBundles.set(name, r.status());
  });
  page.on('console', (m) => { if (m.type() === 'error') console.log(`[${name}] console: ${m.text()}`); });
  await page.addInitScript(trackConnections);
  await page.goto(url);
  await page.getByTestId('connect-identity-input').fill(name);
  await page.getByTestId('connect-password-input').fill('Voice-test-passw0rd');
  await page.getByTestId('connect-button').click();
  await page.getByTestId('server-view').waitFor({ timeout: 60_000 });
  await page
    .getByTestId('channel-item')
    .filter({ has: page.getByText('General Voice', { exact: true }) })
    .first()
    // aria-disabled there means "can't be dragged" (members can't reorder
    // channels), not "can't be joined", so Playwright's check is skipped.
    .click({ force: true });
  console.log(`${name} joined General Voice`);
  return page;
};

const inboundAudioBytes = (page) =>
  page.evaluate(async () => {
    let total = 0;
    for (const pc of window.__pcs) {
      const stats = await pc.getStats();
      stats.forEach((r) => {
        if (r.type === 'inbound-rtp' && r.kind === 'audio') total += r.bytesReceived || 0;
      });
    }
    return { total, connections: window.__pcs.length };
  });

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let failed = false;
try {
  const a = await join('voice-a');
  const b = await join('voice-b');
  await sleep(8_000);
  for (const [name, page] of [['voice-a', a], ['voice-b', b]]) {
    const first = await inboundAudioBytes(page);
    await sleep(5_000);
    const second = await inboundAudioBytes(page);
    console.log(`${name}: ${first.connections} connections, inbound audio ${first.total} -> ${second.total} bytes`);
    if (!(second.total > first.total && second.total > 0)) {
      failed = true;
      console.error(`${name}: no audio arriving`);
      await page.screenshot({ path: `voice-${name}.png` });
    }
    if (expectShiver) {
      const status = shiverBundles.get(name);
      console.log(`${name}: Shiver's client bundle answered ${status}`);
      if (status !== 200) {
        failed = true;
        console.error(`${name}: Shiver's client didn't load in the browser`);
      }
    }
  }
} catch (e) {
  failed = true;
  console.error(e);
} finally {
  await browser.close();
}
console.log(failed ? 'VOICE TEST FAILED' : 'VOICE TEST PASSED');
process.exit(failed ? 1 : 0);
