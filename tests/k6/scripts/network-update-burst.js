// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// k6 scenario: Network Update burst (p99 под нагрузкой).
// setup() создает пул из N networks → default() делает PATCH random network.
// ВАЖНО: Update в kacho-vpc делает sync Get + AssertFolderOwnership ПЕРЕД
// созданием Operation — поэтому latency включает sync repo.Get.

import { Trend, Counter } from 'k6/metrics';
import { FOLDER_ID, post, patch, get, del, uid, expect200 } from './lib/client.js';
import { pollOperation } from './lib/poll-op.js';

const POOL_SIZE = 500;

export const options = {
  setupTimeout: '120s',
  scenarios: {
    update_burst: {
      executor: 'ramping-arrival-rate',
      startRate: 500,
      timeUnit: '1s',
      preAllocatedVUs: 300,
      maxVUs: 2000,
      stages: [
        { duration: '30s', target: 1000 },
        { duration: '30s', target: 2000 },
        { duration: '30s', target: 3000 },
        { duration: '1m',  target: 4000 },
        { duration: '30s', target: 0 },
      ],
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.05'],
  },
};

export function setup() {
  const ids = [];
  for (let i = 0; i < POOL_SIZE; i++) {
    const res = post('/vpc/v1/networks', { folderId: FOLDER_ID, name: uid('upool') });
    if (res.status === 200) {
      const op = pollOperation(res.json('id'));
      if (op.ok) {
        const nid = res.json('metadata.networkId');
        if (nid) ids.push(nid);
      }
    }
  }
  console.log(`setup: created ${ids.length} networks for update-pool`);
  return { ids };
}

export default function (data) {
  if (!data.ids || data.ids.length === 0) return;
  const id = data.ids[Math.floor(Math.random() * data.ids.length)];
  patch(`/vpc/v1/networks/${id}`, { updateMask: 'description', description: `upd-${Date.now()}` });
  // НЕ poll — измеряем sync-возврат latency Update (он = sync Get + ops INSERT + return)
}

export function teardown(data) {
  for (const id of data.ids) {
    const res = del(`/vpc/v1/networks/${id}`);
    if (res.status === 200) pollOperation(res.json('id'));
  }
}
