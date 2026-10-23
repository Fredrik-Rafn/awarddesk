const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const path=require('node:path');
const source=fs.readFileSync(path.join(__dirname,'../../internal/web/assets/http.js'),'utf8');
const response=(status,data)=>({status,ok:status>=200&&status<300,json:async()=>data});
function client(fetch){return vm.runInNewContext(source+'\nworkspaceHTTP;',{fetch})}
test('restart refreshes token and performs each action exactly once',async()=>{
  let refresh=0,actions=0,rejected=0;
  const api=client(async(url,options)=>{
    if(url==='/api/config'){refresh++;await new Promise(r=>setTimeout(r,5));return response(200,{csrf:'a'.repeat(64)})}
    if(options.headers['X-CSRF-Token']!=='a'.repeat(64)){rejected++;return response(403,{error:'invalid request token',code:'request_token_invalid'})}
    actions++;return response(200,{saved:true});
  });
  const result=await Promise.all([api.request('/api/admin/settings',{method:'PUT',body:'{}'}),api.request('/api/searches',{method:'POST',body:'{}'})]);
  assert.equal(refresh,1);assert.equal(rejected,2);assert.equal(actions,2);assert.equal(result.length,2);
});
test('provider rejection never replays a mutation',async()=>{
  let requests=0;const api=client(async()=>{requests++;return response(403,{error:'provider rejected the session',code:'session_denied'})});
  await assert.rejects(api.request('/api/admin/accounts',{method:'PUT'}),e=>e.status===403);assert.equal(requests,1);
});
test('lost response is never replayed',async()=>{
  let requests=0;const api=client(async()=>{requests++;throw Error('connection interrupted')});
  await assert.rejects(api.request('/api/searches',{method:'POST'}));assert.equal(requests,1);
});
test('token recovery is bounded to one replay',async()=>{
  let actions=0,refresh=0;const api=client(async(url)=>{if(url==='/api/config'){refresh++;return response(200,{csrf:'b'.repeat(64)})}actions++;return response(403,{code:'request_token_invalid'})});
  await assert.rejects(api.request('/api/admin/accounts',{method:'PUT'}),e=>e.status===403);assert.equal(actions,2);assert.equal(refresh,1);
});

test('unchanged search polls reuse the snapshot without parsing an empty response',async()=>{
  let requests=0;
  const api=client(async(url,options)=>{
    requests++;
    if(requests===1){assert.equal(options.headers['If-None-Match'],undefined);return {...response(200,{id:'job',status:'running'}),headers:{get:name=>name==='ETag'?'"first"':null}}}
    if(requests===2){assert.equal(options.headers['If-None-Match'],'"first"');return {status:304,ok:false,json:async()=>{throw Error('must not parse 304')}}}
    assert.equal(options.headers['If-None-Match'],'"first"');return {...response(200,{id:'job',status:'completed'}),headers:{get:name=>name==='ETag'?'"second"':null}};
  });
  const first=await api.request('/api/searches/job');
  assert.equal((await api.request('/api/searches/job')),first);
  assert.equal((await api.request('/api/searches/job')).status,'completed');
});

test('search snapshot storage is bounded and never conditions mutations',async()=>{
  const api=client(async(url,options)=>{
    if(options.method==='DELETE'){assert.equal(options.headers['If-None-Match'],undefined);return response(204,null)}
    if(url==='/api/searches/0')assert.equal(options.headers['If-None-Match'],undefined);
    return {...response(200,{id:url}),headers:{get:name=>name==='ETag'?'"snapshot"':null}};
  });
  for(let i=0;i<5;i++)await api.request('/api/searches/'+i);
  await api.request('/api/searches/0');
  await api.request('/api/searches/4',{method:'DELETE'});
});
