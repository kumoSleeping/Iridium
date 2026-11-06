// What the composer offers to store as a credential (src/chat/secretish.ts), and what it leaves.
import { expect, test } from "vitest";
import { entropy, findSecretish } from "../src/chat/secretish";
import { glimpse } from "../src/chat/savesecret";

test("known keys, with the names they go by", () => {
  const gh = "ghp_" + "aB3dE6gH9jK2mN5pQ8sT1vW4yZ7bC0eF3hI6";
  expect(findSecretish(`push with ${gh} please`)).toEqual({ value: gh, start: 10, end: 10 + gh.length, name: "GITHUB_TOKEN" });
  expect(findSecretish("key sk-ant-api03-abcdefghijklmnopqrstuvwxyz012345")?.name).toBe("ANTHROPIC_API_KEY");
  expect(findSecretish("key sk-proj-abcdefghijklmnopqrstuvwxyz012345")?.name).toBe("OPENAI_API_KEY");
  expect(findSecretish("AKIAIOSFODNN7EXAMPLE")?.name).toBe("AWS_ACCESS_KEY_ID");
  const pem = "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----";
  expect(findSecretish(`my key:\n${pem}\nthanks`)?.value).toBe(pem);
});

test("a long random run counts; hashes, ids, paths, links and words do not", () => {
  expect(findSecretish("the password is Xk9mP2vQ7rT4wZ8nB3cF6hJ1")?.name).toBe("SECRET");
  for (const s of [
    "commit 3f2a9c4e8b7d6f5a4e3d2c1b0a9f8e7d6c5b4a39",
    "session e8da99de-6598-433b-8ba3-57d7ee36efac",
    "see /home/iridium/kumo/Iridium/internal/server/routes_secrets.go",
    "https://example.com/aB3dE6gH9jK2mN5pQ8sT1vW4yZ7bC0eF3hI6",
    "useSecretsPageWithLongCamelCaseIdentifierName",
    "a {{GITHUB_TOKEN}} already",
    "just words here",
  ])
    expect(findSecretish(s), s).toBeNull();
});

test("one said not to be one is passed over", () => {
  const v = "Xk9mP2vQ7rT4wZ8nB3cF6hJ1";
  expect(findSecretish(v, new Set([v]))).toBeNull();
  expect(entropy("aaaa")).toBe(0);
  expect(glimpse(v)).toBe("Xk9m…J1");
  expect(glimpse("short")).toBe("…");
});
