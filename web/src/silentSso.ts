import { api } from "./api";

export type OIDCConfig = {
  enabled: boolean;
  issuer?: string;
  autoLogin?: boolean;
};

// sessionStorage rather than localStorage: a silent attempt is scoped to this
// tab's browsing session, so a fresh tab tries again while a reload after a
// refusal does not.
const ATTEMPTED_KEY = "vendra.sso.silentAttempted";
const SIGNED_OUT_KEY = "vendra.sso.signedOut";

// Paths where a silent attempt must never start. The first three are where a
// loop would come from; the rest never render this application at all, and
// an invitation link has to reach the registration form, not the provider.
const NEVER_HERE = ["/login", "/register", "/api", "/mcp", "/health"];

function excludedPath(pathname: string): boolean {
  return NEVER_HERE.some((p) => pathname === p || pathname.startsWith(p + "/"));
}

function readFlag(key: string): boolean {
  try {
    return window.sessionStorage.getItem(key) === "true";
  } catch {
    // Private modes and blocked site data throw. Reading that as "already
    // attempted" is the safe answer: reading it as "not yet" is a redirect
    // loop, because the flag written on the way out would be lost too.
    return true;
  }
}

function writeFlag(key: string, value: boolean) {
  try {
    if (value) window.sessionStorage.setItem(key, "true");
    else window.sessionStorage.removeItem(key);
  } catch {
    /* nothing to do; readFlag already fails closed */
  }
}

/**
 * Records that the person signed out on purpose. Signing them straight back in
 * would make the logout look broken, so auto-login stays off until a session
 * exists again.
 */
export function markSignedOut() {
  writeFlag(SIGNED_OUT_KEY, true);
  writeFlag(ATTEMPTED_KEY, true);
}

/** Clears both suppressions once a session exists again. */
export function clearSilentSsoState() {
  writeFlag(SIGNED_OUT_KEY, false);
  writeFlag(ATTEMPTED_KEY, false);
}

/** Keeps the post-sign-in destination on this site; the server checks again. */
export function safeReturnTo(raw: string): string {
  return raw.startsWith("/") && !raw.startsWith("//") && !raw.startsWith("/\\")
    ? raw
    : "/";
}

/**
 * Decides whether to try signing in without showing a login screen.
 *
 * It must never run more than once per browsing session: prompt=none either
 * answers with a code at once or comes back with login_required, and retrying
 * that on every page load bounces the browser between here and the provider
 * for as long as the visitor keeps watching the screen flicker.
 */
export function shouldAttemptSilentSso(
  config: OIDCConfig | undefined,
  location: Pick<Location, "pathname" | "search">,
): boolean {
  if (!config?.enabled || !config.autoLogin) return false;
  if (excludedPath(location.pathname)) return false;
  if (readFlag(SIGNED_OUT_KEY)) return false;
  if (readFlag(ATTEMPTED_KEY)) return false;
  // The callback appends this marker when the provider had no session, so a
  // refusal is remembered even if sessionStorage was cleared in between.
  if (new URLSearchParams(location.search).has("sso")) return false;
  return true;
}

/** Sends the browser to the provider for a silent attempt, a top-level move. */
export function beginSilentSso(returnTo: string) {
  writeFlag(ATTEMPTED_KEY, true);
  window.location.assign(
    `/api/auth/oidc/start?prompt=none&returnTo=${encodeURIComponent(safeReturnTo(returnTo))}`,
  );
}

/**
 * Asks the server whether a silent attempt is wanted from here. Any failure
 * to answer means no: the login screen still works without it.
 */
export async function silentSsoWanted(
  location: Pick<Location, "pathname" | "search">,
): Promise<boolean> {
  try {
    return shouldAttemptSilentSso(
      await api<OIDCConfig>("/api/auth/oidc/config"),
      location,
    );
  } catch {
    return false;
  }
}
