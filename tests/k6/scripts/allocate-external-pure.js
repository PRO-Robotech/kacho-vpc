// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// k6 scenario: ЧИСТЫЙ AllocateExternalIP throughput — без poll, без cleanup.
// Цель: найти потолок external-IP-allocate/sec на 1 pod (interval-sweep по rate).
//
// Требует default EXTERNAL_PUBLIC AddressPool для зоны ZONE_ID с БОЛЬШИМ CIDR
// (например 10.0.0.0/8 = 16M IP) — иначе пул исчерпается и пойдут ResourceExhausted.
//   curl -XPOST $BASE/vpc/v1/addressPools -d '{"name":"lt-ext","cidrBlocks":["10.0.0.0/8"],
//        "kind":"EXTERNAL_PUBLIC","zoneId":"zone-d","isDefault":true}'
//
// Cleanup после прогона (snapshot rows велик):
//   psql -c "DELETE FROM addresses WHERE name LIKE 'adrx-%';
//            DELETE FROM operations WHERE description LIKE 'Create address adrx-%';"
//
// Замечание: каждый allocate в worker'е делает FolderClient.GetCloudID (gRPC,
// не кешируется) → ceiling ≈ ~3000/sec на 1 pod (ниже, чем у Network.Create).

import { Counter } from 'k6/metrics';
import { FOLDER_ID, post, uid, expect200 } from './lib/client.js';

const ZONE = __ENV.ALLOC_ZONE_ID || 'zone-d';

export const options = {
  scenarios: {
    alloc_sweep: {
      executor: 'ramping-arrival-rate',
      startRate: 500,
      timeUnit: '1s',
      preAllocatedVUs: 200,
      maxVUs: 2000,
      stages: [
        { duration: '40s', target: 1000 },
        { duration: '40s', target: 2000 },
        { duration: '40s', target: 3000 },
        { duration: '40s', target: 5000 },   // выше потолка — RPS упрется в ~3000
        { duration: '30s', target: 0 },
      ],
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.02'],
    http_req_duration: ['p(99)<2000'],
  },
};

const allocated = new Counter('external_ips_allocated');

export default function () {
  const res = post('/vpc/v1/addresses', {
    folderId: FOLDER_ID,
    name: uid('adrx'),
    externalIpv4AddressSpec: { zoneId: ZONE },
  });
  if (expect200(res, 'alloc-pure')) allocated.add(1);
}
