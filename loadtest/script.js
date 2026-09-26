import http from 'k6/http';
import { check } from 'k6';
import exec from 'k6/execution';

// We will pass your API URL in through the terminal
const API_URL = __ENV.API_URL; 

export const options = {
  stages: [
    { duration: '5s', target: 1000 }, // Ramp up to 1,000 users in 5 seconds
    { duration: '10s', target: 1000 }, // Hold the attack at 1,000 users for 10 seconds
    { duration: '5s', target: 0 },    // Ramp down
  ],
};

// 1. SETUP: This runs once before the attack starts to seed the 500 TVs
export function setup() {
  console.log(`Seeding database at ${API_URL}...`);
  http.post(`${API_URL}/admin/products`);
}

// 2. THE ATTACK: This function runs thousands of times per second
export default function () {
  // We simulate "network retries" by deliberately reusing the exact same Idempotency-Key 10% of the time!
  const isRetry = Math.random() < 0.10;
  const idempotencyKey = isRetry 
    ? `loadtest-order-${exec.vu.idInTest}` 
    : `loadtest-order-${exec.vu.idInTest}-${exec.scenario.iterationInTest}`;

  // Smash the Buy button
  const res = http.post(`${API_URL}/orders`, null, {
    headers: { 'Idempotency-Key': idempotencyKey },
  });

  // Verify the API didn't crash (500 Error). We expect 201 (Success), 200 (Retry), or 409 (Sold Out).
  check(res, {
    'API did not crash': (r) => [201, 200, 409].includes(r.status),
  });
}