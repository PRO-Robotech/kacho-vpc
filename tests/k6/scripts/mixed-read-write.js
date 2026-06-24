// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// k6 scenario: Production-like mixed workload.
// 60% Get/List + 30% Create + 10% Delete.

import { sleep } from 'k6';
import { FOLDER_ID, post, get, del, uid, expect200 } from './lib/client.js';
import { pollOperation } from './lib/poll-op.js';

export const options = {
  stages: [
    { duration: '1m', target: 30 },
    { duration: '5m', target: 30 },
    { duration: '30s', target: 0 },
  ],
  thresholds: {
    http_req_failed: ['rate<0.02'],
    http_req_duration: ['p(99)<2000'],
    'http_reqs{op:read}': ['count>0'],
    'http_reqs{op:create}': ['count>0'],
  },
};

// Хранилище созданных id для последующего delete
const createdIds = [];

export default function () {
  const r = Math.random();
  if (r < 0.6) {
    // READ — list или get
    get(`/vpc/v1/networks?folderId=${FOLDER_ID}&pageSize=20`, { tags: { op: 'read' } });
  } else if (r < 0.9) {
    // CREATE
    const res = post('/vpc/v1/networks', { folderId: FOLDER_ID, name: uid('mix') });
    if (res.status === 200) {
      const op = pollOperation(res.json('id'));
      if (op.ok) {
        const nid = res.json('metadata.networkId');
        if (nid) createdIds.push(nid);
      }
    }
  } else {
    // DELETE (если есть что удалить)
    const id = createdIds.shift();
    if (id) {
      const res = del(`/vpc/v1/networks/${id}`);
      if (res.status === 200) pollOperation(res.json('id'));
    }
  }
  sleep(0.05);
}

export function teardown() {
  // Final cleanup
  for (const id of createdIds) {
    const res = del(`/vpc/v1/networks/${id}`);
    if (res.status === 200) pollOperation(res.json('id'));
  }
}
