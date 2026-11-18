import { defineConfig } from "@playwright/test";

const port = process.env.ATHENAEUM_E2E_PORT ?? "8091";
const origin = `http://localhost:${port}`;

export default defineConfig({
  testDir: "tests",
  fullyParallel: false,
  workers: 1,
  reporter: "list",
  timeout: 30_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: origin,
    channel: process.env.ATHENAEUM_E2E_CHANNEL ?? "chrome",
    trace: "retain-on-failure",
  },
  webServer: {
    command: "sh start-server.sh",
    url: `${origin}/api/health`,
    reuseExistingServer: false,
    timeout: 180_000,
  },
});
