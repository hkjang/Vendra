import { describe, expect, it } from "vitest";
import { APIError, sessionUnavailable } from "./api";

describe("sessionUnavailable", () => {
  it("treats a refusal as being signed out", () => {
    expect(sessionUnavailable(new APIError(401, "unauthenticated"))).toBe(false);
  });

  it("treats a server error as an unanswered question", () => {
    for (const status of [500, 502, 503, 504]) {
      expect(sessionUnavailable(new APIError(status, "database error"))).toBe(true);
    }
  });

  it("treats a request that never arrived as an unanswered question", () => {
    // fetch rejects with a TypeError when the connection drops, which is not
    // an APIError at all. Requiring an APIError with a 5xx sent that case to
    // the sign-in form — telling someone they were logged out when the app had
    // simply been unable to ask.
    expect(sessionUnavailable(new TypeError("Failed to fetch"))).toBe(true);
    expect(sessionUnavailable(new Error("network"))).toBe(true);
    expect(sessionUnavailable(undefined)).toBe(true);
    expect(sessionUnavailable("something")).toBe(true);
  });

  it("does not read some other 4xx as a refusal", () => {
    // Only 401 says the credentials were rejected. A 403 or a 404 on the
    // session endpoint means something else went wrong, and claiming the
    // person is signed out would be a guess.
    for (const status of [400, 403, 404, 429]) {
      expect(sessionUnavailable(new APIError(status, "no"))).toBe(true);
    }
  });
});
