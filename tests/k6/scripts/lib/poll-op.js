// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// LRO polling helper для k6.

import { get } from './client.js';
import { check, sleep } from 'k6';

const POLL_MAX_ATTEMPTS = 20;
const POLL_INTERVAL_SEC = 0.1;  // 100ms

export function pollOperation(opId) {
  for (let i = 0; i < POLL_MAX_ATTEMPTS; i++) {
    const res = get(`/operations/${opId}`);
    if (res.status !== 200) {
      return { ok: false, code: res.status, body: res.body };
    }
    const j = res.json();
    if (j.done) {
      return { ok: !j.error, error: j.error, response: j.response, raw: j };
    }
    sleep(POLL_INTERVAL_SEC);
  }
  return { ok: false, code: 'timeout', timeoutAfter: POLL_MAX_ATTEMPTS * POLL_INTERVAL_SEC };
}

export function expectOpDone(opResult, label) {
  return check(opResult, {
    [`${label}: op completed`]: (r) => r.ok === true,
  });
}
