/* global URL, process */
const { test } = require('node:test');
const assert = require('node:assert/strict');
const m = require('./build-metadata.cjs');
const base = {version:1,kind:'manifest',repository:'owner/repo',workflow_path:'.github/workflows/ci.yml',run_id:1,run_number:2,run_attempt:2,event:'push',source_sha:'a'.repeat(40),execution_sha:'b'.repeat(40),pr_number:null,selected:[{service:'api',image_name:'api'}]};
const result = (attempt,state='success',pushed=true) => ({...base,kind:'result',run_attempt:attempt,service:'api',image_name:'api',source_attempt:attempt,state,pushed});
test('aggregate fences partial reruns and newest results', () => {
  const cases = [
    {name:'retained success',records:[result(1)],matrix:'success',state:'success'},
    {name:'matrix failed before publication',records:[result(1)],matrix:'failure',state:'running'},
    {name:'new failure wins',records:[result(1),result(2,'failure',false)],matrix:'failure',state:'failure'},
    {name:'new progress wins',records:[result(1),result(2,'running',false)],matrix:'success',state:'running'},
    {name:'compile only',records:[result(2,'success',false)],matrix:'success',state:'success'},
    {name:'cancelled',records:[result(2,'cancelled',false)],matrix:'cancelled',state:'cancelled'}
  ];
  for (const c of cases) assert.equal(m.aggregate(base,c.records,c.matrix).results[0].state,c.state,c.name);
});
test('rejects conflicting or foreign records', () => {
  for (const records of [[result(2),{...result(2),pushed:false}], [{...result(2),execution_sha:'c'.repeat(40)}], [{...result(2),image_name:'other'}]]) assert.throws(() => m.aggregate(base,records,'success'));
});
test('push outcomes use step IDs and explicit push permission', () => {
  for (const c of [
    {steps:{cache_push:{outcome:'success'}},allowed:true,pushed:true},
    {steps:{image_push:{outcome:'success'}},allowed:false,pushed:false},
    {steps:{image_push:{outcome:'failure'}},allowed:true,pushed:false}
  ]) assert.equal(m.outcome(c.steps,c.allowed,'success').pushed,c.pushed);
});
test('pagination preserves independent concurrent results', async () => {
  const pages = {1:Array.from({length:100},(_,i)=>({id:i})),2:[{id:100}]};
  const records = await m.listChecks(async path => ({check_runs:pages[Number(new URL('https://api.github.com'+path).searchParams.get('page'))]}),'owner/repo',base.source_sha);
  assert.equal(records.length,101);
});
test('selection excludes internal empty-matrix sentinel and rejects ambiguous selections', () => {
  const cases = [
    {matrix:'[{"service":"none","image_name":"none"}]',has:'false',expected:[]},
    {matrix:'[{"service":"api","image_name":"api"}]',has:'true',expected:[{service:'api',image_name:'api'}]},
    {matrix:'[]',has:undefined,error:true},
    {matrix:'[{"service":"none","image_name":"none"}]',has:'true',error:true},
    {matrix:'[{"service":"api","image_name":"api"},{"service":"api","image_name":"api"}]',has:'true',error:true}
  ];
  for(const c of cases) {
    if(c.error) assert.throws(()=>m.selection(c.matrix,c.has));
    else assert.deepEqual(m.selection(c.matrix,c.has),c.expected);
  }
});
test('shared fixture is an explicit empty selection', () => {
  const fixture=require('./testdata/build-metadata-v1.json');
  assert.deepEqual(m.aggregate(fixture,[],'skipped'),{...fixture,kind:'final',results:[]});
});
test('immutable run boundaries reject stale and foreign results', () => {
  for(const patch of [
    {repository:'other/repo'}, {workflow_path:'.github/workflows/wrong.yml'},
    {source_sha:'c'.repeat(40)}, {run_id:42}, {run_number:42},
    {run_attempt:3,source_attempt:3}, {selected:[]}, {source_attempt:1}
  ]) assert.throws(()=>m.aggregate(base,[{...result(2),...patch}],'success'));
});
test('final publication requires a matching manifest and authoritative app provenance', async () => {
  const fs=require('node:fs'); const os=require('node:os'); const path=require('node:path');
  const dir=fs.mkdtempSync(path.join(os.tmpdir(),'metadata-test-'));
  const eventPath=path.join(dir,'event.json'); fs.writeFileSync(eventPath,'{}');
  const originalFetch=globalThis.fetch; const originalArgs=process.argv;
  const env={GITHUB_EVENT_PATH:eventPath,GITHUB_REPOSITORY:base.repository,GITHUB_WORKFLOW_REF:'owner/repo/.github/workflows/ci.yml@refs/heads/main',GITHUB_RUN_ID:'1',GITHUB_RUN_NUMBER:'2',GITHUB_RUN_ATTEMPT:'2',GITHUB_EVENT_NAME:'push',GITHUB_SHA:base.execution_sha,MATRIX_JSON:JSON.stringify(base.selected),HAS_BUILDS:'true',MATRIX_RESULT:'success',NEEDS_JSON:'{"docker":{"result":"success"}}'};
  const current={...base,source_sha:base.execution_sha};
  const record={...current,kind:'result',service:'api',image_name:'api',state:'success',pushed:true,source_attempt:2};
  const manifest={name:'build-metadata',app:{id:15368},head_sha:current.source_sha,external_id:m.externalID(current),status:'completed',conclusion:'neutral',output:{summary:JSON.stringify(current)}};
  const transport={...manifest,name:'build-metadata-result',external_id:m.externalID(record),output:{summary:JSON.stringify(record)}};
  const retained={...record,run_attempt:1,source_attempt:1};
  const retainedCheck={...transport,external_id:m.externalID(retained),output:{summary:JSON.stringify(retained)}};
  const finalRecord={...current,kind:'final',results:[{service:'api',image_name:'api',state:'success',pushed:true,source_attempt:1}]};
  const cases=[
    {name:'retained clone created after final',checks:[manifest,{...manifest,external_id:m.externalID(finalRecord),output:{text:JSON.stringify(finalRecord),summary:'Final'}},retainedCheck,{...retainedCheck,id:999}],sourceAttempt:1},
    {name:'failed rerun before first publication',checks:[manifest,retainedCheck],matrix:'failure',error:true},
    {name:'real reexecution running supersedes retained clone',checks:[manifest,retainedCheck,{...transport,output:{text:JSON.stringify({...record,state:'running',pushed:false}),summary:'Running'}}],error:true},
    {name:'real reexecution failure supersedes retained clone',checks:[manifest,retainedCheck,{...transport,output:{text:JSON.stringify({...record,state:'failure',pushed:false}),summary:'Failed'}}],error:true},
    {name:'missing manifest',checks:[transport],error:true},
    {name:'valid',checks:[manifest,transport]},
    {name:'wrong app',checks:[manifest,{...transport,app:{id:9}}],error:true},
    {name:'post-final retained clone',checks:[manifest,transport,{...transport,id:999,started_at:'later'}]},
    {name:'canonical text',checks:[manifest,{...transport,output:{text:JSON.stringify(record),summary:'Build result'}}]},
    {name:'mixed transport',checks:[{...manifest,output:{text:JSON.stringify(current),summary:'Manifest'}},transport]},
    {name:'conflicting dual payload',checks:[manifest,{...transport,output:{text:JSON.stringify(record),summary:JSON.stringify({...record,pushed:false})}}],error:true},
    {name:'conflicting replica',checks:[manifest,transport,{...transport,output:{summary:JSON.stringify({...record,pushed:false})}}],error:true},
    {name:'different raw replica',checks:[manifest,transport,{...transport,output:{summary:JSON.stringify(record,null,2)}}],error:true},
    {name:'different transport status',checks:[manifest,transport,{...transport,status:'in_progress'}],error:true},
    {name:'wrong external identity',checks:[manifest,{...transport,external_id:m.externalID(record)+'wrong'}],error:true},
    {name:'wrong source',checks:[manifest,{...transport,head_sha:'c'.repeat(40)}],error:true},
    {name:'empty canonical text',checks:[manifest,{...transport,output:{text:'',summary:JSON.stringify(record)}}],error:true},
    {name:'wrong manifest selection',checks:[{...manifest,output:{summary:JSON.stringify({...current,selected:[]})}},transport],error:true}
  ];
  try {
    process.argv=['node','build-metadata.cjs','final'];
    for(const c of cases) {
      const published=[];
      globalThis.fetch=async (url,options)=>{
        const route=new URL(url).pathname;
        let data;
        if(options.method === 'POST') { published.push(JSON.parse(options.body)); data={id:100}; }
        else if(route.includes('/actions/runs/')) data={run_attempt:2,head_sha:current.execution_sha,check_suite_id:10};
        else if(route.includes('/check-suites/')) data={app:{id:15368}};
        else data={check_runs:c.checks};
        return {ok:true,json:async()=>data};
      };
      if(c.error) { await assert.rejects(m.main({...env,MATRIX_RESULT:c.matrix || 'success'}),undefined,c.name); assert.equal(published.length,0); }
      else { await m.main(env); assert.equal(published[0].conclusion,'neutral'); assert.equal(JSON.parse(published[0].output.text).kind,'final'); assert.equal(JSON.parse(published[0].output.text).results[0].source_attempt,c.sourceAttempt || 2); }
    }
  } finally { globalThis.fetch=originalFetch;process.argv=originalArgs;fs.rmSync(dir,{recursive:true,force:true}); }
});
test('empty selection must be explicit rather than missing matrix output', () => {
  for(const c of [{matrix:undefined,has:'false'},{matrix:'{}',has:'false'},{matrix:'[]',has:'true'},{matrix:'[{"service":"api","image_name":"api"}]',has:'false'}]) assert.throws(()=>m.selection(c.matrix,c.has));
});
test('fork PRs preserve native CI without attempting metadata writes in any publisher mode', async () => {
  const fs=require('node:fs'); const os=require('node:os'); const path=require('node:path');
  const dir=fs.mkdtempSync(path.join(os.tmpdir(),'metadata-fork-'));
  const eventPath=path.join(dir,'event.json');
  fs.writeFileSync(eventPath,JSON.stringify({pull_request:{number:7,head:{repo:{full_name:'contributor/repo'},sha:'c'.repeat(40)}}}));
  const originalArgs=process.argv;
  try {
    for(const mode of ['manifest','running','result','final']) {
      process.argv=['node','build-metadata.cjs',mode];
      // No selection, token, API endpoint or output path: read-only runs must
      // return before any publisher setup, including result-ID validation.
      await assert.doesNotReject(m.main({GITHUB_EVENT_PATH:eventPath,GITHUB_REPOSITORY:'owner/repo',GITHUB_EVENT_NAME:'pull_request'}),mode);
    }
  } finally { process.argv=originalArgs;fs.rmSync(dir,{recursive:true,force:true}); }
});
test('authorized same-repository publication including Dependabot still fails on API errors', async () => {
  const fs=require('node:fs'); const os=require('node:os'); const path=require('node:path');
  const dir=fs.mkdtempSync(path.join(os.tmpdir(),'metadata-authorized-'));
  const eventPath=path.join(dir,'event.json');
  const originalArgs=process.argv; const originalFetch=globalThis.fetch;
  const env={GITHUB_EVENT_PATH:eventPath,GITHUB_REPOSITORY:'owner/repo',GITHUB_WORKFLOW_REF:'owner/repo/.github/workflows/ci.yml@refs/heads/main',GITHUB_RUN_ID:'1',GITHUB_RUN_NUMBER:'2',GITHUB_RUN_ATTEMPT:'1',GITHUB_EVENT_NAME:'pull_request',GITHUB_SHA:'b'.repeat(40),MATRIX_JSON:'[]',HAS_BUILDS:'false'};
  try {
    process.argv=['node','build-metadata.cjs','manifest'];
    globalThis.fetch=async()=>({ok:false,status:403});
    for(const actor of ['contributor','dependabot[bot]']) {
      fs.writeFileSync(eventPath,JSON.stringify({sender:{login:actor},pull_request:{number:7,head:{repo:{full_name:'owner/repo'},sha:'a'.repeat(40)}}}));
      await assert.rejects(m.main({...env,GITHUB_ACTOR:actor}),/Checks API failed \(403\)/);
    }
  } finally { process.argv=originalArgs;globalThis.fetch=originalFetch;fs.rmSync(dir,{recursive:true,force:true}); }
});
