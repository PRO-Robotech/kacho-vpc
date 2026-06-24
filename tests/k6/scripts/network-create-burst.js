// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// k6 scenario: Network Create burst.
// Профиль: ramp-up 0→50 VU за 2 мин, hold 50 VU 3 мин, ramp-down.
// SLO: p99 < 1500ms, error rate < 1%, ≥ 30 Create/sec sustained.

import { sleep } from 'k6';
import { Trend, Counter, Rate } from 'k6/metrics';
import { FOLDER_ID, post, del, uid, expect200 } from './lib/client.js';
import { pollOperation } from './lib/poll-op.js';

export const options = {
  stages: [
    { duration: '2m', target: 50 },
    { duration: '3m', target: 50 },
    { duration: '30s', target: 0 },
  ],
  thresholds: {
    http_req_failed:   ['rate<0.01'],
    http_req_duration: ['p(99)<1500'],
    iteration_duration: ['p(95)<3000'],
    'lro_completion_time': ['p(99)<3000'],
  },
};

const lroLatency = new Trend('lro_completion_time', true);
const opErrors = new Counter('op_errors');

export default function () {
  const name = uid('net');
  const createStart = Date.now();
  const createRes = post('/vpc/v1/networks', { folderId: FOLDER_ID, name });
  if (!expect200(createRes, 'create')) return;

  const opId = createRes.json('id');
  const networkId = createRes.json('metadata.networkId');
  const op = pollOperation(opId);
  lroLatency.add(Date.now() - createStart);

  if (!op.ok) {
    opErrors.add(1);
    return;
  }

  // Cleanup: Delete created network
  if (networkId) {
    const delRes = del(`/vpc/v1/networks/${networkId}`);
    if (delRes.status === 200) {
      pollOperation(delRes.json('id'));
    }
  }
  sleep(0.05);
}
