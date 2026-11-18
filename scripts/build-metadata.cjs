/* global process, fetch, Buffer, console */
'use strict';
const fs = require('node:fs');
function requireValid(ok, message) { if (!ok) throw new Error(message); }
function selection(matrix, hasBuilds) {
  requireValid(hasBuilds === 'true' || hasBuilds === 'false', 'Missing build selection');
  const rows = JSON.parse(matrix);
  requireValid(Array.isArray(rows), 'Invalid matrix');
  if (hasBuilds === 'false') {
    requireValid(rows.length === 0 || (rows.length === 1 && rows[0].service === 'none'), 'Inconsistent empty matrix');
    return [];
  }
  requireValid(rows.length > 0, 'Empty build matrix');
  const selected = rows.map(({service,image_name}) => ({service,image_name}));
  const seen = new Set();
  for (const row of selected) {
    requireValid(/^[a-z0-9][a-z0-9_-]*$/.test(row.service) && row.service !== 'none' && /^[a-z0-9][a-z0-9_-]*$/.test(row.image_name) && !seen.has(row.service), 'Invalid selection');
    seen.add(row.service);
  }
  return selected;
}
const immutable = ['version','repository','workflow_path','run_id','run_number','event','source_sha','execution_sha','pr_number'];
function sameRun(a,b) { return immutable.every(key => a[key] === b[key]) && JSON.stringify(a.selected) === JSON.stringify(b.selected); }
function aggregate(base, records, matrix) {
  requireValid(Array.isArray(base.selected), 'Missing selection');
  const latest = new Map();
  const seen = new Set();
  for (const r of records) {
    requireValid(sameRun(base,r) && r.kind === 'result' && Number.isSafeInteger(r.run_attempt) && r.run_attempt > 0 && r.run_attempt <= base.run_attempt && r.source_attempt === r.run_attempt, 'Invalid result identity');
    requireValid(base.selected.some(s => s.service === r.service && s.image_name === r.image_name), 'Unexpected service');
    requireValid(['running','success','failure','cancelled'].includes(r.state) && typeof r.pushed === 'boolean' && (!r.pushed || r.state === 'success'), 'Invalid result state');
    const key = `${r.run_attempt}/${r.service}`;
    requireValid(!seen.has(key), 'Duplicate result'); seen.add(key);
    if (!latest.has(r.service) || latest.get(r.service).run_attempt < r.run_attempt) latest.set(r.service,r);
  }
  const results = base.selected.map(s => {
    const r = latest.get(s.service);
    if (!r || (r.run_attempt < base.run_attempt && (matrix !== 'success' || r.state !== 'success'))) return {...s,state:'running',pushed:false,source_attempt:base.run_attempt};
    const {state,pushed,source_attempt,image_refs,digest} = r;
    return {...s,state,pushed,source_attempt,...(image_refs ? {image_refs} : {}),...(digest ? {digest} : {})};
  });
  return {...base,kind:'final',results};
}
function outcome(steps, allowed, jobState) {
  const pushes = [steps.cache_push?.outcome,steps.image_push?.outcome];
  const state = jobState === 'cancelled' ? 'cancelled' : jobState === 'failure' || pushes.includes('failure') ? 'failure' : 'success';
  return {state,pushed:state === 'success' && allowed && pushes.includes('success')};
}
function externalID(m) { return `build-metadata:v1:${m.repository}:${m.run_id}:${m.run_attempt}:${m.kind === 'result' ? m.service : m.kind}`; }
async function listChecks(api,repo,sha) {
  const all = [];
  for (let page=1; ;page++) {
    const data = await api(`/repos/${repo}/commits/${sha}/check-runs?filter=all&per_page=100&page=${page}`);
    all.push(...data.check_runs);
    if (data.check_runs.length < 100) return all;
  }
}
// Only v1 records from this immutable run enter this compatibility reader.
// Pre-repair producers used summary; text, when present, is authoritative.
function payload(check) {
  const output = check.output || {};
  const hasText = output.text !== null && output.text !== undefined;
  const raw = hasText ? output.text : output.summary;
  requireValid(typeof raw === 'string' && Buffer.byteLength(raw) <= 65536, 'Invalid metadata payload');
  const value = JSON.parse(raw);
  if (hasText && typeof output.summary === 'string' && /^\s*[[{]/.test(output.summary)) {
    requireValid(JSON.stringify(JSON.parse(output.summary)) === JSON.stringify(value), 'Conflicting metadata transports');
  }
  return {raw,value};
}
async function main(env=process.env) {
  const event = JSON.parse(fs.readFileSync(env.GITHUB_EVENT_PATH,'utf8'));
  // Fork pull_request tokens cannot create checks even with checks: write.
  // Apply this before setup in every mode so native verification remains usable.
  // Same-repository Dependabot PRs retain their explicit job permissions.
  if (env.GITHUB_EVENT_NAME === 'pull_request' && event.pull_request.head.repo.full_name !== env.GITHUB_REPOSITORY) {
    console.log('Build metadata publication skipped for read-only fork PR.');
    return;
  }
  const base = {version:1,kind:'manifest',repository:env.GITHUB_REPOSITORY,workflow_path:env.GITHUB_WORKFLOW_REF.split('@')[0].split('/').slice(2).join('/'),run_id:Number(env.GITHUB_RUN_ID),run_number:Number(env.GITHUB_RUN_NUMBER),run_attempt:Number(env.GITHUB_RUN_ATTEMPT),event:env.GITHUB_EVENT_NAME,source_sha:event.pull_request?.head.sha || env.GITHUB_SHA,execution_sha:env.GITHUB_SHA,pr_number:event.pull_request?.number || null,selected:selection(env.MATRIX_JSON,env.HAS_BUILDS)};
  const api = async (path,method='GET',body) => {
    const response = await fetch(`${env.GITHUB_API_URL || 'https://api.github.com'}${path}`,{method,headers:{Authorization:`Bearer ${env.GITHUB_TOKEN}`,Accept:'application/vnd.github+json','X-GitHub-Api-Version':'2022-11-28'},...(body ? {body:JSON.stringify(body)} : {})});
    requireValid(response.ok,`Checks API failed (${response.status})`);
    return response.json();
  };
  let metadata = base;
  const mode = process.argv[2];
  if (mode === 'running' || mode === 'result') {
    const service = JSON.parse(env.SERVICE_JSON);
    requireValid(base.selected.some(s => s.service === service.service && s.image_name === service.image_name),'Service outside selection');
    const progress = mode === 'running' ? {state:'running',pushed:false} : outcome(JSON.parse(env.STEPS_JSON),env.PUSH_ALLOWED === 'true',env.JOB_STATUS);
    metadata = {...base,kind:'result',service:service.service,image_name:service.image_name,...progress,source_attempt:base.run_attempt};
    if (progress.pushed) {
      const tag = base.event === 'pull_request' ? `tree-${env.TREE_SHA}` : base.execution_sha;
      requireValid(/^[a-f0-9]{40}$/.test(base.event === 'pull_request' ? env.TREE_SHA : tag),'Invalid image tag');
      metadata.image_refs = [`ghcr.io/${base.repository.split('/')[0].toLowerCase()}/${service.image_name}:${tag}`];
      if (env.IMAGE_DIGEST) metadata.digest = env.IMAGE_DIGEST;
    }
  } else if (mode === 'final') {
    const run = await api(`/repos/${base.repository}/actions/runs/${base.run_id}`);
    requireValid(run.run_attempt === base.run_attempt && run.head_sha === (event.pull_request?.head.sha || base.execution_sha), 'Workflow identity changed');
    const suite = await api(`/repos/${base.repository}/check-suites/${run.check_suite_id}`);
    const checks = await listChecks(api,base.repository,base.source_sha);
    const manifests = checks.filter(check => check.name === 'build-metadata' && check.app?.id === suite.app?.id && check.head_sha === base.source_sha && check.external_id?.startsWith(`build-metadata:v1:${base.repository}:${base.run_id}:`));
    requireValid(manifests.length > 0, 'Missing manifest');
    for (const check of manifests) {
      const manifest = payload(check).value;
      requireValid(sameRun(base,manifest) && ['manifest','final'].includes(manifest.kind) && manifest.run_attempt > 0 && manifest.run_attempt <= base.run_attempt && check.external_id === externalID(manifest) && check.status === 'completed' && check.conclusion === 'neutral', 'Invalid manifest');
    }
    const records = [];
    const replicas = new Map();
    for (const check of checks) {
      if (check.name !== 'build-metadata-result') continue;
      // Other runs on this commit are unrelated. Our external ID is immutable.
      if (!check.external_id?.startsWith(`build-metadata:v1:${base.repository}:${base.run_id}:`)) continue;
      const {raw,value:r} = payload(check);
      requireValid(check.app?.id === suite.app?.id && check.head_sha === base.source_sha, 'Invalid result provenance');
      requireValid(sameRun(base,r) && r.kind === 'result' && Number.isSafeInteger(r.run_attempt) && r.run_attempt > 0 && r.run_attempt <= base.run_attempt && r.source_attempt === r.run_attempt, 'Invalid result identity');
      requireValid(base.selected.some(s => s.service === r.service && s.image_name === r.image_name), 'Unexpected service');
      requireValid(check.external_id === externalID(r) && check.status === 'completed' && check.conclusion === 'neutral','Invalid transport');
      // GitHub can clone a retained check after the final aggregate. IDs and
      // timestamps change, but the bytes and immutable envelope must agree.
      const key = `${r.run_attempt}/${r.service}`;
      const fingerprint = JSON.stringify([raw,check.external_id,check.name,check.app.id,check.head_sha,check.status,check.conclusion]);
      if (replicas.has(key)) requireValid(replicas.get(key) === fingerprint, 'Conflicting result replica');
      else { replicas.set(key,fingerprint); records.push(r); }
    }
    metadata = aggregate(base,records,env.MATRIX_RESULT);
    requireValid(metadata.results.every(r => r.state === 'success'), 'Incomplete or failed matrix results');
    const needs = JSON.parse(env.NEEDS_JSON);
    requireValid(Object.values(needs).every(n => ['success','skipped'].includes(n.result)), 'Upstream failure');
  } else requireValid(mode === 'manifest','Invalid publisher mode');
  const summary = JSON.stringify(metadata);
  requireValid(Buffer.byteLength(summary) <= 65536,'Metadata too large');
  const body = {name:metadata.kind === 'result' ? 'build-metadata-result' : 'build-metadata',head_sha:base.source_sha,external_id:externalID(metadata),status:'completed',conclusion:'neutral',output:{title:'Build metadata v1',summary:'Version 1 build transport',text:summary}};
  const id = mode === 'result' ? Number(env.RESULT_CHECK_ID) : 0;
  requireValid(mode !== 'result' || id > 0,'Missing running check');
  const check = await api(`/repos/${base.repository}/check-runs${id ? '/'+id : ''}`,id ? 'PATCH' : 'POST',body);
  if (mode === 'running') fs.appendFileSync(env.GITHUB_OUTPUT,`check_id=${check.id}\n`);
}
module.exports = {selection,aggregate,outcome,listChecks,externalID,main};
if (require.main === module) main().catch(error => { console.error(error.message); process.exitCode=1; });
