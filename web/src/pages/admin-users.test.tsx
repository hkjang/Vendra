import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { UsersPanel } from "./Admin";
import { api } from "../api";

vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  api: vi.fn(),
}));

const call = vi.mocked(api);

const user = (email: string) => ({
  id: email,
  email,
  displayName: email,
  userType: "internal",
  status: "active",
  locale: "ko",
  timezone: "Asia/Seoul",
  roles: [],
});

/**
 * The search queries per keystroke behind a debounce, so two answers can be in
 * flight at once. Before the sequence guard, the slower one landed last and the
 * table showed rows for a query the box no longer held — on the screen whose
 * actions are role assignment, password reset and deactivation.
 */
describe("UsersPanel search", () => {
  beforeEach(() => {
    call.mockReset();
  });

  // Real timers: the component debounces with setTimeout and waitFor schedules
  // its own, so faking them deadlocks the two against each other.
  const settle = () => new Promise((resolve) => setTimeout(resolve, 350));

  it("ignores an answer that arrives after a later search", async () => {
    let releaseFirstSearch: (value: {
      items: unknown[];
      truncated: boolean;
    }) => void = () => {};
    call.mockImplementation((path: string) => {
      if (path.startsWith("/api/v1/admin/users?q=")) {
        const query = decodeURIComponent(path.split("q=")[1]);
        if (query === "kim") {
          return new Promise((resolve) => {
            releaseFirstSearch = resolve;
          }) as ReturnType<typeof api>;
        }
        if (query === "kimchul") {
          return Promise.resolve({ items: [], truncated: false }) as ReturnType<
            typeof api
          >;
        }
        return Promise.resolve({
          items: [user("first@vendra.test")],
          truncated: false,
        }) as ReturnType<typeof api>;
      }
      return Promise.resolve({ items: [], canShare: false }) as ReturnType<
        typeof api
      >;
    });

    render(
      <MemoryRouter>
        <UsersPanel />
      </MemoryRouter>,
    );
    await settle();
    await waitFor(() =>
      expect(screen.queryAllByText("first@vendra.test").length).toBeGreaterThan(
        0,
      ),
    );

    const box = screen.getByLabelText("사용자 검색");
    fireEvent.change(box, { target: { value: "kim" } });
    await settle();

    // The narrower search overtakes the one still in flight.
    fireEvent.change(box, { target: { value: "kimchul" } });
    await settle();
    await waitFor(() =>
      expect(screen.queryByText("first@vendra.test")).toBeNull(),
    );

    // The earlier answer finally arrives carrying a match for "kim".
    releaseFirstSearch({ items: [user("kim@vendra.test")], truncated: false });
    await settle();

    expect((box as HTMLInputElement).value).toBe("kimchul");
    expect(screen.queryAllByText("kim@vendra.test").length).toBe(0);
  });
});
