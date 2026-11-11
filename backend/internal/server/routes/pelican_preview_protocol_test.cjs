const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

const shell = fs.readFileSync(0, 'utf8');
const maxBytes = Number(process.argv[2]);
const scripts = [...shell.matchAll(/<script>([\s\S]*?)<\/script>/g)];
assert.equal(scripts.length, 1, 'the bootstrap must be a single complete inline script');
const bootstrap = new vm.Script(scripts[0][1]);

function setup(topLevel = false) {
  const listeners = new Map();
  const writes = [];
  const operations = [];
  const acknowledgements = [];
  const parent = {
    postMessage(message, targetOrigin) {
      assert.equal(listeners.has('message'), false, 'remove listener before acknowledging acceptance');
      acknowledgements.push({ message: { ...message }, targetOrigin });
      operations.push('ack');
    },
  };
  const window = topLevel ? parent : {};
  let handler;
  const context = vm.createContext({
    parent, window, URL, TextEncoder,
    location: { href: 'https://preview.example/api/v1/pelican-preview', origin: 'null' },
    addEventListener(type, callback) {
      listeners.set(type, callback);
      if (type === 'message') handler = callback;
    },
    removeEventListener(type, callback) {
      assert.equal(listeners.get(type), callback);
      listeners.delete(type);
    },
    document: {
      open() {
        assert.equal(listeners.has('message'), false, 'remove listener before executing output');
        operations.push('open');
      },
      write(html) { operations.push('write'); writes.push(html); },
      close() { operations.push('close'); },
    },
  });
  bootstrap.runInContext(context);
  return {
    listeners, writes, operations, acknowledgements,
    send(data, overrides = {}) {
      handler?.({ source: parent, origin: 'https://preview.example', data, ...overrides });
    },
  };
}

const output = '<!doctype html><html><body onload="this.dataset.loaded=1"><svg></svg><canvas></canvas><script>window.generated=true;</script></body></html>';
const message = { type: 'xiass-pelican-render', html: output };
const invalid = [
  [message, { source: {} }],
  [message, { source: null }],
  [message, { origin: 'https://attacker.example' }],
  [message, { origin: 'null' }],
  [message, { origin: 'http://preview.example' }],
  [message, { origin: 'https://preview.example:444' }],
  [null], [42], ['html'], [[]], [{}],
  [{ type: 'other', html: output }],
  [{ type: 'xiass-pelican-render', html: {} }],
  [{ type: 'xiass-pelican-render', html: '' }],
  [{ type: 'xiass-pelican-render', html: 'x'.repeat(maxBytes + 1) }],
  [{ type: 'xiass-pelican-render', html: '\u4e2d'.repeat(Math.floor(maxBytes / 3) + 1) }],
];
const renderer = setup();
for (const [data, overrides] of invalid) {
  renderer.send(data, overrides);
  assert.equal(renderer.writes.length, 0, 'invalid messages must not consume or render the shell');
  assert.equal(renderer.acknowledgements.length, 0, 'invalid, oversized or foreign messages must not be acknowledged');
  assert.equal(renderer.listeners.has('message'), true);
}
renderer.send(message);
const accepted = {
  message: { type: 'xiass-pelican-render-accepted' },
  targetOrigin: 'https://preview.example',
};
assert.deepEqual(renderer.acknowledgements, [accepted], 'acknowledge once to the exact parent origin without HTML or private fields');
assert.deepEqual(renderer.operations, ['ack', 'open', 'write', 'close']);
assert.equal(renderer.writes.length, 1);
assert.ok(renderer.writes[0].startsWith(output), 'preserve scripts, SVG, canvas and inline handlers');
const sizing = renderer.writes[0].slice(output.length);
assert.ok(sizing.startsWith('<script>'));
assert.ok(sizing.endsWith('</script>'));
new vm.Script(sizing.slice('<script>'.length, -'</script>'.length));
renderer.send({ ...message, html: '<p>replacement</p>' });
assert.equal(renderer.writes.length, 1, 'even an already queued duplicate cannot render again');
assert.equal(renderer.acknowledgements.length, 1, 'a queued duplicate must not send another acknowledgement');

for (const html of ['x'.repeat(maxBytes), '\u4e2d'.repeat(Math.floor(maxBytes / 3)) + 'x'.repeat(maxBytes % 3)]) {
  const boundary = setup();
  boundary.send({ type: 'xiass-pelican-render', html });
  assert.equal(new TextEncoder().encode(html).byteLength, maxBytes);
  assert.equal(boundary.writes.length, 1, 'accept the exact saved-result byte limit');
  assert.deepEqual(boundary.acknowledgements, [accepted]);
}
const standalone = setup(true);
assert.equal(standalone.listeners.size, 0, 'a top-level visit must stay empty');
standalone.send(message);
assert.equal(standalone.writes.length, 0);
assert.equal(standalone.acknowledgements.length, 0);
