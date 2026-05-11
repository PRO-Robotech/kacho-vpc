// k6 scenario: ЧИСТЫЙ Network Create throughput — без poll, без cleanup.
// Меряет sync-возврат латентность и max Create/sec на 1 pod.
// Cleanup делается в teardown() в конце.

import { Trend, Counter } from 'k6/metrics';
import { FOLDER_ID, post, del, uid, expect200 } from './lib/client.js';

export const options = {
  scenarios: {
    create_burst: {
      executor: 'ramping-arrival-rate',  // целимся в фиксированный RPS, не VU
      startRate: 50,
      timeUnit: '1s',
      preAllocatedVUs: 100,
      maxVUs: 400,
      stages: [
        { duration: '1m', target: 100 },   // 100 Create/sec
        { duration: '1m', target: 200 },   // 200 Create/sec
        { duration: '1m', target: 400 },   // 400 Create/sec
        { duration: '1m', target: 600 },   // 600 Create/sec
        { duration: '30s', target: 0 },
      ],
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.05'],
    http_req_duration: ['p(99)<2000'],
  },
};

const created = new Counter('networks_created');

export default function () {
  const res = post('/vpc/v1/networks', { folderId: FOLDER_ID, name: uid('netp') });
  if (expect200(res, 'create-pure')) {
    created.add(1);
    // НЕ poll, НЕ delete — измеряем чистый sync-throughput Create
  }
}

export function teardown() {
  // Best-effort cleanup: список созданных за прогон. Postman/k6 не хранит state
  // между iterations глобально без shared array — поэтому полагаемся на то,
  // что test-folder будет очищен внешним cleanup-скриптом после прогона.
  // Здесь — no-op (cleanup делается отдельно cleanup-test-folder.sh).
}
