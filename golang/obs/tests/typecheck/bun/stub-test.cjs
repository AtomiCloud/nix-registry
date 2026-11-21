'use strict';
const assert = require('node:assert/strict');
const stub = process.argv[2];
const fresh = () => {
  delete require.cache[require.resolve(stub)];
  return require(stub);
};
{
  const port = fresh();
  const table = Object.assign(Object.create(null), {
    contract: 'normalized:contract.yaml',
    hash: 'x',
    metrics: {},
    events: {},
    spans: {},
  });
  assert.equal(port.register(table).ok, true);
  assert.equal(port.register(table).ok, true, 'repeat compares normalized clones');
  console.log('PASS stub normalized clone repeat');
}
{
  const port = fresh();
  assert.equal(
    port.register({
      contract: 'owner:contract.yaml',
      hash: 'x',
      metrics: {},
      events: { 'already.owned': 'error' },
      spans: {},
    }).ok,
    true,
  );
  let reads = 0;
  const table = {
    contract: 'getter:contract.yaml',
    hash: 'x',
    metrics: {},
    get events() {
      return ++reads === 1 ? { 'safe.fresh': 'error' } : { 'already.owned': 'error' };
    },
    spans: {},
  };
  assert.equal(port.register(table).ok, true);
  assert.equal(reads, 1, 'no caller property reads after the owned snapshot is captured');
  console.log('PASS stub captures caller properties once before ownership');
}
