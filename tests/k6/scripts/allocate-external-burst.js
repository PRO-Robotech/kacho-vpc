// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// k6 scenario: AllocateExternalIP burst.
// SLO: p99 < 600ms, ≥ 50 alloc/sec при < 80% utilization.

import { FOLDER_ID, ZONE_ID, post, del, uid, expect200 } from './lib/client.js';
import { pollOperation } from './lib/poll-op.js';

export const options = {
  stages: [
    { duration: '1m', target: 30 },
    { duration: '3m', target: 30 },
    { duration: '30s', target: 0 },
  ],
  thresholds: {
    http_req_failed: ['rate<0.005'],
    http_req_duration: ['p(99)<800'],
  },
};

export default function () {
  const name = uid('adr');
  const res = post('/vpc/v1/addresses', {
    folderId: FOLDER_ID,
    name,
    externalIpv4AddressSpec: { zoneId: ZONE_ID },
  });
  if (!expect200(res, 'allocate')) return;
  const op = pollOperation(res.json('id'));
  if (!op.ok) return;
  const aid = res.json('metadata.addressId');
  if (aid) {
    const delRes = del(`/vpc/v1/addresses/${aid}`);
    if (delRes.status === 200) pollOperation(delRes.json('id'));
  }
}
