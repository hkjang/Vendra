import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  beginSilentSso,
  clearSilentSsoState,
  markSignedOut,
  safeReturnTo,
  shouldAttemptSilentSso,
} from "./silentSso";

const on = { enabled: true, autoLogin: true };
const home = { pathname: "/", search: "" };

// prompt=none either answers with a code or comes back with login_required,
// and neither draws a screen. Retrying after the second answer sends the
// browser back to the provider on every load, so each rule here is one of the
// guards that keep a signed-out visitor from watching the screen flicker.
describe("shouldAttemptSilentSso", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("tries once per tab session when the administrator allows it", () => {
    expect(shouldAttemptSilentSso(on, home)).toBe(true);
    const assign = vi.fn();
    vi.spyOn(window, "location", "get").mockReturnValue({
      ...window.location,
      assign,
    } as Location);
    beginSilentSso("/suppliers/42?tab=risk");
    expect(assign).toHaveBeenCalledWith(
      "/api/auth/oidc/start?prompt=none&returnTo=%2Fsuppliers%2F42%3Ftab%3Drisk",
    );
    // The reload after a refusal is the load that would loop.
    expect(shouldAttemptSilentSso(on, home)).toBe(false);
  });

  it("does nothing while the setting is off, which is the default", () => {
    expect(shouldAttemptSilentSso({ enabled: true }, home)).toBe(false);
    expect(shouldAttemptSilentSso({ enabled: true, autoLogin: false }, home)).toBe(false);
    expect(shouldAttemptSilentSso({ enabled: false, autoLogin: true }, home)).toBe(false);
    expect(shouldAttemptSilentSso(undefined, home)).toBe(false);
  });

  it("does not sign a person back in after they signed out", () => {
    markSignedOut();
    expect(shouldAttemptSilentSso(on, home)).toBe(false);
    // A session again — a password login, or a fresh sign-in — lifts it.
    clearSilentSsoState();
    expect(shouldAttemptSilentSso(on, home)).toBe(true);
  });

  it("honours the marker the callback leaves in the address", () => {
    // Storage may have been cleared between the refusal and this load; the
    // address is the guard that survives that.
    expect(shouldAttemptSilentSso(on, { pathname: "/login", search: "?sso=none" })).toBe(false);
    expect(shouldAttemptSilentSso(on, { pathname: "/", search: "?sso=none" })).toBe(false);
  });

  it("never starts from the login, callback, or registration paths", () => {
    for (const pathname of [
      "/login",
      "/register",
      "/api/auth/oidc/callback",
      "/api/v1/me",
      "/mcp",
      "/health/ready",
    ]) {
      expect(shouldAttemptSilentSso(on, { pathname, search: "" })).toBe(false);
    }
    // A deep link is exactly where a silent sign-in should start.
    expect(shouldAttemptSilentSso(on, { pathname: "/suppliers/42", search: "" })).toBe(true);
    expect(shouldAttemptSilentSso(on, { pathname: "/registered", search: "" })).toBe(true);
  });

  it("treats storage it cannot read as already attempted", () => {
    // Private modes throw here. Reading that as "not yet" would loop: the
    // flag written on the way out would be lost the same way.
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("blocked", "SecurityError");
    });
    expect(shouldAttemptSilentSso(on, home)).toBe(false);
  });
});

describe("safeReturnTo", () => {
  it("keeps the destination on this site", () => {
    expect(safeReturnTo("/suppliers/42")).toBe("/suppliers/42");
    expect(safeReturnTo("//evil.example/")).toBe("/");
    expect(safeReturnTo("/\\evil.example")).toBe("/");
    expect(safeReturnTo("https://evil.example")).toBe("/");
    expect(safeReturnTo("")).toBe("/");
  });
});
