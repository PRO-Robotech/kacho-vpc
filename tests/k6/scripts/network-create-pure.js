// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// k6 scenario: ЧИСТЫЙ Network Create throughput — без poll, без cleanup.
// Цель: определить max Create/sec на 1 pod (target 5000+/sec).
// Cleanup: внешний cleanup-скрипт после прогона (psql DELETE WHERE name LIKE 'netp-%').

import { Counter } from 'k6/metrics';
import { FOLDER_ID, post, uid, expect200 } from './lib/client.js';

export const options = {
  scenarios: {
    create_burst: {
      executor: 'ramping-arrival-rate',  // фиксированный target RPS, не VU
      startRate: 500,
      timeUnit: '1s',
      preAllocatedVUs: 500,
      maxVUs: 4000,
      stages: [
        { duration: '30s', target: 1000 },   // 1000 Create/sec
        { duration: '30s', target: 2000 },   // 2000 Create/sec
        { duration: '30s', target: 3000 },   // 3000 Create/sec
        { duration: '30s', target: 4000 },   // 4000 Create/sec
        { duration: '1m',  target: 5000 },   // 5000 Create/sec — ТАРГЕТ
        { duration: '1m',  target: 5000 },   // hold 5000/sec
        { duration: '30s', target: 0 },
      ],
    },
  },
  thresholds: {
    http_req_failed:   ['rate<0.05'],   // < 5% errors
    http_req_duration: ['p(99)<2000'],  // p99 < 2s
    iterations:        ['count>500000'],
  },
};

const created = new Counter('networks_created');

export default function () {
  const res = post('/vpc/v1/networks', { folderId: FOLDER_ID, name: uid('netp') });
  if (expect200(res, 'create-pure')) {
    created.add(1);
  }
}
