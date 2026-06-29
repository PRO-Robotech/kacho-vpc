#!/usr/bin/env node
// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

/*
 * run-incremental.js — прогон newman-сьюты ПО ОДНОМУ КЕЙСУ за раз.
 *
 * Зачем: kacho-vpc quota-guarded — пачка из ~731 кейса разом создает
 * сотни ресурсов одновременно и упирается в quota cap. Здесь: один project → результат →
 * (если упал — зачистка тест-папок) → следующий. Низкий resource-footprint в любой момент.
 * Один newman-процесс (library API) — без per-case process startup, поэтому быстро.
 *
 * Кейсы самоочищаются (cleanup-step в конце), но не все полностью (некоторые `*-CR-CRUD-*`
 * оставляют pre-network/pre-subnet) — поэтому periodic-cleanup каждые N кейсов + final cleanup
 * + initial cleanup (стереть накопленный мусор от прошлых прогонов).
 *
 * Usage:
 *   tests/newman/scripts/run-incremental.sh                 # все кейсы, с нуля
 *   tests/newman/scripts/run-incremental.sh --resume        # продолжить (пропустить уже сделанные)
 *   tests/newman/scripts/run-incremental.sh --service subnet # только один сервис
 *   tests/newman/scripts/run-incremental.sh --cleanup-only  # только зачистить тест-папки и выйти
 *   ENV=environments/custom.postman_environment.json ... .sh # свой env-файл
 *   CLEANUP_EVERY=25  DELAY_REQUEST=30  ... .sh             # тюнинг
 *
 * Outputs (tests/newman/out/incremental/):
 *   progress.tsv  — case-id \t PASS|FAIL \t assertions \t failed \t requests \t ms
 *   failed/<id>.json — newman summary упавшего кейса (для разбора)
 *   summary.txt   — итоговая сводка
 */
'use strict';
const fs = require('fs');
const path = require('path');
const newman = require('newman');

const ROOT = path.resolve(__dirname, '..');
const ENV_FILE = path.join(ROOT, process.env.ENV || 'environments/local.postman_environment.json');
const COLLECTIONS_DIR = path.join(ROOT, 'collections');
const OUT = path.join(ROOT, 'out/incremental');
const PROGRESS = path.join(OUT, 'progress.tsv');
const SUMMARY = path.join(OUT, 'summary.txt');
const CLEANUP_EVERY = parseInt(process.env.CLEANUP_EVERY || '25', 10);
const DELAY_REQUEST = parseInt(process.env.DELAY_REQUEST || '30', 10);
const ALL_SERVICES_DEFAULT = ['network','subnet','address','route-table','security-group','gateway','operation','internal-pool','internal-cloud'];
// SERVICES env-override: список сервисов через пробел/запятую (напр. прогон без internal-*)
const ALL_SERVICES = process.env.SERVICES ? process.env.SERVICES.split(/[\s,]+/).filter(Boolean) : ALL_SERVICES_DEFAULT;

const args = process.argv.slice(2);
const RESUME = args.includes('--resume');
const CLEANUP_ONLY = args.includes('--cleanup-only');
const FAILED_ONLY = args.includes('--failed'); // прогнать только кейсы, помеченные FAIL в текущем progress.tsv (для точечного re-run после фикса)
const svcIdx = args.indexOf('--service');
const ONLY_SERVICE = svcIdx >= 0 ? args[svcIdx + 1] : null;
const casesIdx = args.indexOf('--cases');
// явный список case-id'ов: --cases C1,C2,... или env CASES="C1 C2 ..." (имеет приоритет над --service/SERVICES)
let ONLY_CASES = null;
{
  const raw = (casesIdx >= 0 ? args[casesIdx + 1] : '') || process.env.CASES || '';
  const ids = raw.split(/[\s,]+/).filter(Boolean);
  if (ids.length) ONLY_CASES = new Set(ids);
}

fs.mkdirSync(path.join(OUT, 'failed'), { recursive: true });

// --- env ---
const env = JSON.parse(fs.readFileSync(ENV_FILE, 'utf8'));
const ev = (k) => { const v = env.values.find(x => x.key === k); return v ? v.value : undefined; };
const BASE = (ev('baseUrl') || '').replace(/\/$/, '');
const TEST_PROJECTS = env.values.filter(x => /^existingProject/.test(x.key)).map(x => x.value).filter(Boolean);
if (!BASE) { console.error('нет baseUrl в env'); process.exit(2); }
if (!TEST_PROJECTS.length) { console.error('нет existingProject* в env'); process.exit(2); }

// --- cleanup тест-папок (FK-safe порядок; async-Delete'ы fire-and-forget, несколько проходов) ---
// [restPath, listKey, filterFn?]
const KINDS = [
  ['addresses',      'addresses'],
  ['routeTables',    'routeTables'],
  ['securityGroups', 'securityGroups', (r) => r && r.defaultForNetwork === false], // default уходит с network
  ['subnets',        'subnets'],
  ['gateways',       'gateways'],
  ['networks',       'networks'],
];
async function jget(url) {
  try { const r = await fetch(url); if (!r.ok) return null; return await r.json(); } catch { return null; }
}
async function jdel(url) {
  try { await fetch(url, { method: 'DELETE' }); } catch { /* ignore */ }
}
async function cleanupPass(passes = 3) {
  let removedTotal = 0;
  for (let p = 0; p < passes; p++) {
    let removed = 0;
    for (const fid of TEST_PROJECTS) {
      for (const [restPath, listKey, filterFn] of KINDS) {
        const j = await jget(`${BASE}/vpc/v1/${restPath}?projectId=${encodeURIComponent(fid)}&pageSize=1000`);
        const arr = (j && Array.isArray(j[listKey])) ? j[listKey] : [];
        for (const r of arr) {
          if (filterFn && !filterFn(r)) continue;
          if (!r || !r.id) continue;
          await jdel(`${BASE}/vpc/v1/${restPath}/${encodeURIComponent(r.id)}`);
          removed++;
        }
      }
    }
    removedTotal += removed;
    if (removed === 0) break;
    await sleep(1500); // дать worker'ам додавить async Delete перед следующим проходом
  }
  return removedTotal;
}
async function remainingCount() {
  let n = 0;
  for (const fid of TEST_PROJECTS) for (const [restPath, listKey, filterFn] of KINDS) {
    const j = await jget(`${BASE}/vpc/v1/${restPath}?projectId=${encodeURIComponent(fid)}&pageSize=1000`);
    const arr = (j && Array.isArray(j[listKey])) ? j[listKey] : [];
    n += arr.filter(r => !filterFn || filterFn(r)).length;
  }
  return n;
}
const sleep = (ms) => new Promise(r => setTimeout(r, ms));

// --- enumerate cases (top-level projects в каждой коллекции) ---
// targeted = true → перебираем ВСЕ сервисы (чтобы найти любой case-id), потом отфильтруем по ONLY_CASES.
function enumerateCases(targeted) {
  const services = (targeted || !ONLY_SERVICE) ? ALL_SERVICES_DEFAULT : [ONLY_SERVICE];
  const cases = [];
  for (const svc of services) {
    const col = path.join(COLLECTIONS_DIR, `${svc}.postman_collection.json`);
    if (!fs.existsSync(col)) { if (!targeted) console.error(`[skip] нет коллекции ${svc}`); continue; }
    const c = JSON.parse(fs.readFileSync(col, 'utf8'));
    for (const item of (c.item || [])) {
      const name = item.name;
      const id = name.split(' — ')[0].split(' - ')[0].trim() || name;
      cases.push({ svc, collection: col, projectName: name, id });
    }
  }
  return cases;
}

// --- run one project через newman library ---
function runProject(tc) {
  return new Promise((resolve) => {
    const t0 = Date.now();
    newman.run({
      collection: tc.collection,
      environment: ENV_FILE,
      project: tc.projectName,
      delayRequest: DELAY_REQUEST,
      reporters: [],
      bail: false,
    }, (err, summary) => {
      const ms = Date.now() - t0;
      const a = (summary && summary.run && summary.run.stats && summary.run.stats.assertions) || { total: 0, failed: 0 };
      const rq = (summary && summary.run && summary.run.stats && summary.run.stats.requests) || { total: 0 };
      const runErrs = (summary && summary.run && summary.run.failures) || [];
      const failed = a.failed + (err ? 1 : 0);
      const status = (err || a.failed > 0 || runErrs.length > 0) ? 'FAIL' : 'PASS';
      resolve({ status, assertions: a.total, failed, requests: rq.total, ms, err: err && err.message, failures: runErrs.map(f => ({ name: f.source && f.source.name, err: f.error && f.error.message })) });
    });
  });
}

// --- main ---
(async () => {
  if (CLEANUP_ONLY) {
    console.log(`[cleanup-only] зачистка ${TEST_PROJECTS.length} тест-папок на ${BASE} ...`);
    const n = await cleanupPass(4);
    const left = await remainingCount();
    console.log(`[cleanup-only] удалено ~${n}, осталось ${left}`);
    process.exit(left === 0 ? 0 : 1);
  }

  // --failed: вывести ONLY_CASES из FAIL-строк текущего progress.tsv (baseline полного прогона)
  if (FAILED_ONLY && !ONLY_CASES) {
    if (!fs.existsSync(PROGRESS)) { console.error(`--failed: нет ${PROGRESS} (нужен baseline-прогон)`); process.exit(2); }
    const ids = fs.readFileSync(PROGRESS, 'utf8').split('\n').map(l => l.split('\t')).filter(p => p[1] === 'FAIL').map(p => p[0]);
    if (!ids.length) { console.log('--failed: в progress.tsv нет FAIL — нечего перепрогонять'); process.exit(0); }
    ONLY_CASES = new Set(ids);
  }
  const TARGETED = !!ONLY_CASES;
  // в targeted-режиме пишем в отдельный progress-rerun.tsv, чтобы не затирать baseline
  const progressFile = TARGETED ? path.join(OUT, 'progress-rerun.tsv') : PROGRESS;

  let cases = enumerateCases(TARGETED);
  if (TARGETED) {
    const wanted = new Set(ONLY_CASES);
    cases = cases.filter(tc => wanted.has(tc.id));
    const got = new Set(cases.map(tc => tc.id));
    const missing = [...wanted].filter(id => !got.has(id));
    if (missing.length) console.error(`[targeted] не нашлось case-id'ов в коллекциях: ${missing.join(', ')}`);
  }
  const done = new Set();
  if (!TARGETED && RESUME && fs.existsSync(PROGRESS)) {
    for (const line of fs.readFileSync(PROGRESS, 'utf8').split('\n')) { const id = line.split('\t')[0]; if (id) done.add(id); }
    console.log(`[resume] уже сделано: ${done.size}`);
  } else {
    fs.writeFileSync(progressFile, '');
  }

  console.log(`[incremental${TARGETED ? ' / targeted re-run' : ''}] ${cases.length} кейсов; env=${path.basename(ENV_FILE)}; base=${BASE}; cleanup каждые ${TARGETED ? '∞ (только до/после)' : CLEANUP_EVERY}`);
  process.stdout.write('[initial cleanup] ');
  const ic = await cleanupPass(4);
  console.log(`удалено накопленного мусора ~${ic}, осталось ${await remainingCount()}`);

  let nRun = 0, nPass = 0, nFail = 0, totA = 0, totF = 0, sinceClean = 0;
  const failedCases = [];
  const t0 = Date.now();
  for (const tc of cases) {
    if (done.has(tc.id)) continue;
    const r = await runProject(tc);
    nRun++; sinceClean++; totA += r.assertions; totF += r.failed;
    if (r.status === 'PASS') { nPass++; }
    else {
      nFail++; failedCases.push(tc.id);
      fs.writeFileSync(path.join(OUT, 'failed', `${tc.id}.json`), JSON.stringify({ case: tc.id, ...r }, null, 2));
      // упал — мог оставить ресурсы → зачистить
      await cleanupPass(3); sinceClean = 0;
    }
    fs.appendFileSync(progressFile, `${tc.id}\t${r.status}\t${r.assertions}\t${r.failed}\t${r.requests}\t${r.ms}\n`);
    if (!TARGETED && sinceClean >= CLEANUP_EVERY) { await cleanupPass(2); sinceClean = 0; }
    if (nRun % 20 === 0 || r.status === 'FAIL' || TARGETED) {
      const el = ((Date.now() - t0) / 1000).toFixed(0);
      console.log(`[${nRun}/${cases.length}] pass=${nPass} fail=${nFail} assertions=${totA} (failed ${totF}) | ${el}s | last: ${tc.id} ${r.status}${r.status === 'FAIL' ? ' :: ' + (r.failures.map(f => f.name + ': ' + f.err).join('; ') || r.err) : ''}`);
    }
  }
  process.stdout.write('[final cleanup] ');
  const fc = await cleanupPass(4); const left = await remainingCount();
  console.log(`удалено ~${fc}, осталось ${left}`);

  const el = ((Date.now() - t0) / 1000).toFixed(0);
  const summary = [
    `===== run-incremental${TARGETED ? ' / targeted re-run' : ''}: ${nRun} кейсов за ${el}s =====`,
    `pass=${nPass}  fail=${nFail}  assertions=${totA}  failed-assertions=${totF}`,
    `тест-папки после прогона: осталось ${left} ресурсов (должно быть 0)`,
    failedCases.length ? `FAILED CASES (${failedCases.length}): ${failedCases.join(', ')}` : 'все кейсы зеленые',
    `детали упавших — out/incremental/failed/*.json; прогресс — ${path.relative(ROOT, progressFile)}`,
  ].join('\n');
  fs.writeFileSync(TARGETED ? path.join(OUT, 'summary-rerun.txt') : SUMMARY, summary + '\n');
  console.log('\n' + summary);
  process.exit(nFail === 0 && left === 0 ? 0 : 1);
})().catch(e => { console.error('FATAL', e); process.exit(2); });
