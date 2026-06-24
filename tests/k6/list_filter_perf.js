// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// k6 load test for FGA-filtered ListNetworks throughput / SLA.
//
// SLA targets:
//   - p95 ≤ 100ms total (includes iam.AuthorizeService.ListObjects roundtrip
//     + cache lookup + repo.List + filter).
//   - p99 ≤ 250ms.
//   - Cache hit ratio ≥ 80% steady state.
//
// Setup:
//   - 100 concurrent VUs.
//   - 30 min sustained.
//   - 1000 networks seeded in target project_id.
//   - N = 1..500 FGA-bindings per principal varies per stage.
//
// Run:
//   k6 run \
//     -e BASE_URL=https://api.kacho.local \
//     -e PROJECT_ID=prj_load_test_001 \
//     -e SUBJECT_TOKEN=$KACHO_TEST_TOKEN \
//     tests/k6/list_filter_perf.js
//
// Local dev:
//   k6 run \
//     -e BASE_URL=http://localhost:18080 \
//     -e PROJECT_ID=prj_dev_001 \
//     -e SUBJECT_TOKEN=dev-token \
//     -e DURATION=5m \
//     -e VUS=50 \
//     tests/k6/list_filter_perf.js

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Trend, Counter, Rate } from 'k6/metrics';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:18080';
const PROJECT_ID = __ENV.PROJECT_ID || 'prj_load_test_001';
const TOKEN = __ENV.SUBJECT_TOKEN || '';
const DURATION = __ENV.DURATION || '30m';
const VUS = parseInt(__ENV.VUS || '100', 10);

// Custom metrics.
const listLatency = new Trend('list_filter_latency_ms', true);
const cacheHits = new Counter('cache_hits_total');
const errors = new Counter('list_filter_errors_total');
const emptyResponses = new Rate('empty_response_rate');

export const options = {
  scenarios: {
    steady_state: {
      executor: 'constant-vus',
      vus: VUS,
      duration: DURATION,
      gracefulStop: '30s',
    },
  },
  thresholds: {
    // SLA targets.
    'list_filter_latency_ms': [
      'p(50)<25', // p50 ≤ 25ms (typical cache hit)
      'p(95)<100', // p95 ≤ 100ms (cache miss + FGA roundtrip)
      'p(99)<250', // p99 ≤ 250ms
    ],
    'http_req_failed': ['rate<0.01'], // < 1% errors
    'http_req_duration{tag:list}': ['p(95)<100'],
  },
};

export default function () {
  const url = `${BASE_URL}/vpc/v1/networks?projectId=${PROJECT_ID}&pageSize=100`;
  const params = {
    headers: {
      'Accept': 'application/json',
    },
    tags: { name: 'ListNetworks', tag: 'list' },
  };
  if (TOKEN) {
    params.headers['Authorization'] = `Bearer ${TOKEN}`;
  }

  const start = Date.now();
  const res = http.get(url, params);
  const latency = Date.now() - start;
  listLatency.add(latency);

  const ok = check(res, {
    'status 200': (r) => r.status === 200,
    'has body': (r) => r.body && r.body.length > 0,
  });

  if (!ok) {
    errors.add(1);
    return;
  }

  // Parse body & detect cache hit via cache headers (if backend exposes).
  try {
    const body = res.json();
    if (Array.isArray(body.networks) && body.networks.length === 0) {
      emptyResponses.add(1);
    } else {
      emptyResponses.add(0);
    }
    // Cache hits — heuristic: requests <5ms are likely cache hits.
    if (latency < 5) {
      cacheHits.add(1);
    }
  } catch (e) {
    // Malformed JSON — counted as error.
    errors.add(1);
  }

  // Throughput regulation. With 100 VUs at ~10ms p50 → 10k rps;
  // target — 1000 RPS sustained.
  sleep(0.1);
}

// Summary output formatter — emit machine-readable section to stdout for
// CI/results-md harvest.
export function handleSummary(data) {
  const metrics = data.metrics;
  const summary = {
    p50_ms: metrics.list_filter_latency_ms?.values?.['p(50)'] || 0,
    p95_ms: metrics.list_filter_latency_ms?.values?.['p(95)'] || 0,
    p99_ms: metrics.list_filter_latency_ms?.values?.['p(99)'] || 0,
    requests_total: metrics.http_reqs?.values?.count || 0,
    failures: metrics.http_req_failed?.values?.passes || 0,
    cache_hit_rate: (metrics.cache_hits_total?.values?.count || 0) / Math.max(1, metrics.http_reqs?.values?.count || 1),
    sla_p95_pass: (metrics.list_filter_latency_ms?.values?.['p(95)'] || 0) <= 100,
    sla_p99_pass: (metrics.list_filter_latency_ms?.values?.['p(99)'] || 0) <= 250,
  };
  return {
    'stdout': '\n\n=== List-Filter SLA Summary ===\n' + JSON.stringify(summary, null, 2) + '\n',
    'tests/k6/results/list-filter-sla.json': JSON.stringify(data, null, 2),
  };
}
