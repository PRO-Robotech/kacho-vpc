// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// k6 scenario: List под read-heavy нагрузкой.
// SLO: p99 < 100ms, ≥ 200 RPS sustained.

import { Trend, Rate } from 'k6/metrics';
import { FOLDER_ID, get, expect200 } from './lib/client.js';

export const options = {
  stages: [
    { duration: '30s', target: 50 },
    { duration: '3m', target: 50 },
    { duration: '20s', target: 0 },
  ],
  thresholds: {
    http_req_failed: ['rate<0.001'],
    http_req_duration: ['p(99)<200'],
    iterations: ['count>10000'],
  },
};

export default function () {
  const ops = [
    () => get(`/vpc/v1/networks?folderId=${FOLDER_ID}&pageSize=20`),
    () => get(`/vpc/v1/subnets?folderId=${FOLDER_ID}&pageSize=20`),
    () => get(`/vpc/v1/addresses?folderId=${FOLDER_ID}&pageSize=20`),
    () => get(`/vpc/v1/securityGroups?folderId=${FOLDER_ID}&pageSize=20`),
    () => get(`/vpc/v1/routeTables?folderId=${FOLDER_ID}&pageSize=20`),
  ];
  const op = ops[Math.floor(Math.random() * ops.length)];
  expect200(op(), 'list');
}
