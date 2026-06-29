// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

// Общий HTTP client + auth headers + helpers для k6 нагрузочных тестов kacho-vpc.

import http from 'k6/http';
import { check } from 'k6';

export const BASE_URL = __ENV.BASE_URL || 'http://localhost:18080';
export const FOLDER_ID = __ENV.FOLDER_ID || 'b1gc03zgwksmpe92fd5t';
export const FOLDER_CROSS_ID = __ENV.FOLDER_CROSS_ID || 'b1gv69v0n72te62797mp';
export const ZONE_ID = __ENV.ZONE_ID || 'zone-a';
export const ZONE_ALT_ID = __ENV.ZONE_ALT_ID || 'zone-b';

const ACTOR = __ENV.ACTOR || 'load-test@kacho';
const HEADERS = {
  'Content-Type': 'application/json',
  'x-kacho-actor': ACTOR,
  'x-kacho-folder-id': FOLDER_ID,
};

export function uid(prefix = 'lt') {
  const ts = Date.now().toString(36);
  const rnd = Math.floor(Math.random() * 1e6).toString(36);
  return `${prefix}-${ts}${rnd}`.toLowerCase().replace(/[^a-z0-9-]/g, '');
}

export function post(path, body) {
  return http.post(`${BASE_URL}${path}`, JSON.stringify(body), { headers: HEADERS });
}

export function get(path) {
  return http.get(`${BASE_URL}${path}`, { headers: HEADERS });
}

export function patch(path, body) {
  return http.patch(`${BASE_URL}${path}`, JSON.stringify(body), { headers: HEADERS });
}

export function del(path) {
  return http.del(`${BASE_URL}${path}`, null, { headers: HEADERS });
}

export function expect200(res, label) {
  return check(res, { [`${label}: 200`]: (r) => r.status === 200 });
}
