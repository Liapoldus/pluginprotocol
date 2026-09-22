import { defineConfig } from "vitest/config";

export default defineConfig({
  test: { include: ["unit/**/*.test.ts", "integration/**/*.test.ts"], fileParallelism: false },
});
