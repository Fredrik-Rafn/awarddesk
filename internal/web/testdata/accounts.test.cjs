const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../assets/admin-accounts.js'), 'utf8');
const context = vm.createContext({});
vm.runInContext(source.slice(0, source.indexOf('function accountCells')), context);
const account = {id:'account', enabled:true, email:'test@example.com', outlookEmail:'test@outlook.com', clientId:'test-client'};
const state = session => context.accountModels({accounts:[account], sessions:[{id:'account',...session}]} )[0].state;

test('cooling accounts are scheduled retries rather than manual attention', () => {
  assert.equal(state({state:'failed', needsAttention:false, nextAttempt:new Date(Date.now()+60_000).toISOString()}), 'retrying');
});
test('exhausted attempts require attention and due healthy sessions return to ready', () => {
  assert.equal(state({state:'failed', needsAttention:true}), 'attention');
  assert.equal(state({state:'failed', needsAttention:false, nextAttempt:new Date(Date.now()-60_000).toISOString(),session:{ready:true}}), 'ready');
});
test('ownership takes priority over cooldown display', () => {
  assert.equal(state({state:'preparing',busy:true,nextAttempt:new Date(Date.now()+60_000).toISOString()}),'preparing');
});

test('old onboarding failures do not mark an existing account as manual attention', () => {
  const model = context.accountModels({accounts:[account], sessions:[{id:'account',state:'idle',session:{ready:false}}],accountSetups:[{id:'signup',accountId:'account',status:'failed',updatedAt:new Date().toISOString()}]})[0];
  assert.equal(model.state, 'idle');
});
