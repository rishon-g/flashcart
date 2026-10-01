import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';

// set with -e API_URL=...
const API_URL = __ENV.API_URL; 
// admin token from secrets manager, needed to seed stock
const ADMIN_TOKEN = __ENV.ADMIN_TOKEN;

export const options = {
  stages: [
    { duration: '5s', target: 1000 }, // ramp up to 1,000 users
    { duration: '10s', target: 1000 }, // hold
    { duration: '5s', target: 0 },    // ramp down
  ],
};

// runs once before the test to seed 500 units
export function setup() {
  console.log(`Seeding database at ${API_URL}...`);
  const res = http.post(`${API_URL}/admin/products`, null, {
    headers: { 'X-Admin-Token': ADMIN_TOKEN },
  });
  if (res.status !== 201) {
    exec.test.abort(`Seeding failed with status ${res.status}. Is ADMIN_TOKEN set?`);
  }
}

// each vu runs this in a loop
export default function () {
  // reuse the same key 10% of the time to simulate client retries
  const isRetry = Math.random() < 0.10;
  const idempotencyKey = isRetry 
    ? `loadtest-order-${exec.vu.idInTest}` 
    : `loadtest-order-${exec.vu.idInTest}-${exec.scenario.iterationInTest}`;

  const res = http.post(`${API_URL}/orders`, null, {
    headers: { 'Idempotency-Key': idempotencyKey },
  });

  // 201 new order, 200 replay, 409 sold out. anything else is a failure
  check(res, {
    'API did not crash': (r) => [201, 200, 409].includes(r.status),
  });
}