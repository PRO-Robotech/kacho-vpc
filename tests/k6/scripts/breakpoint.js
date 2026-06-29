// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// k6 scenario: Breakpoint test — линейный ramp до failure.
// Цель: найти capacity (max sustained RPS до violation SLO).

import { sleep } from 'k6';
import { FOLDER_ID, post, get, del, uid, expect200 } from './lib/client.js';
import { pollOperation } from './lib/poll-op.js';

export const options = {
  stages: [
    { duration: '1m', target: 10 },
    { duration: '2m', target: 50 },
    { duration: '2m', target: 100 },
    { duration: '2m', target: 200 },
    { duration: '2m', target: 400 },
    { duration: '2m', target: 800 },
  ],
  thresholds: {
    // Не abort'имся — собираем данные до конца, но фиксируем violation
    http_req_failed: ['rate<0.1'],
    http_req_duration: ['p(99)<5000'],
  },
};

export default function () {
  // Легкая операция — Get/List (не мутация чтобы не overload writes)
  const path = `/vpc/v1/networks?folderId=${FOLDER_ID}&pageSize=10`;
  expect200(get(path), 'list-stress');
  sleep(0.01);
}
