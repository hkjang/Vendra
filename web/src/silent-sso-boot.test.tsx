import { render, screen, waitFor } from "@testing-library/react";
import { BrowserRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { APIError, api } from "./api";

vi.mock("./api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./api")>()),
  api: vi.fn(),
  post: vi.fn(),
}));

const server = vi.mocked(api);

// What the browser does on the first load with no session, which is the only
// moment a silent sign-in may start. The provider is a full-page move, so the
// thing to read is where the window was sent — and that the login form never
// appeared on the way out.
describe("booting without a session", () => {
  let assign: ReturnType<typeof vi.fn>;
  // The address must be set before the snapshot: `assign` is the only thing
  // replaced, but Location has to be copied whole to replace it at all.
  function boot(address: string) {
    window.history.replaceState({}, "", address);
    vi.spyOn(window, "location", "get").mockReturnValue({
      ...window.location,
      assign,
    } as Location);
    render(
      <BrowserRouter>
        <App />
      </BrowserRouter>,
    );
  }
  beforeEach(() => {
    window.sessionStorage.clear();
    assign = vi.fn();
    server.mockReset().mockImplementation(async (path: string) => {
      if (path === "/api/v1/me") throw new APIError(401, "로그인이 필요합니다");
      if (path === "/api/auth/oidc/config")
        return { enabled: true, issuer: "https://keycloak.internal", autoLogin: true };
      return { version: "test" };
    });
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("goes to the provider silently and comes back to the deep link", async () => {
    boot("/suppliers/42?tab=risk");
    await waitFor(() =>
      expect(assign).toHaveBeenCalledWith(
        "/api/auth/oidc/start?prompt=none&returnTo=%2Fsuppliers%2F42%3Ftab%3Drisk",
      ),
    );
    expect(screen.queryByRole("heading", { name: "Vendra에 로그인" })).not.toBeInTheDocument();
  });

  it("shows the login screen where the callback said the provider had no session", async () => {
    boot("/login?sso=none");
    expect(await screen.findByRole("heading", { name: "Vendra에 로그인" })).toBeInTheDocument();
    expect(assign).not.toHaveBeenCalled();
  });

  it("shows the login screen when the administrator has not turned auto-login on", async () => {
    server.mockImplementation(async (path: string) => {
      if (path === "/api/v1/me") throw new APIError(401, "로그인이 필요합니다");
      if (path === "/api/auth/oidc/config") return { enabled: true, issuer: "https://keycloak.internal" };
      return { version: "test" };
    });
    boot("/");
    expect(await screen.findByRole("heading", { name: "Vendra에 로그인" })).toBeInTheDocument();
    expect(assign).not.toHaveBeenCalled();
  });

  it("does not mistake an unanswerable session for a missing one", async () => {
    // A database restart answers 503. Sending everyone to the provider then
    // would sign nobody in and use up the one attempt this tab gets.
    server.mockImplementation(async (path: string) => {
      if (path === "/api/v1/me") throw new APIError(503, "database error");
      if (path === "/api/auth/oidc/config") return { enabled: true, autoLogin: true };
      return { version: "test" };
    });
    boot("/");
    expect(await screen.findByText(/일시적으로 서비스에 연결할 수 없습니다/)).toBeInTheDocument();
    expect(assign).not.toHaveBeenCalled();
  });
});
