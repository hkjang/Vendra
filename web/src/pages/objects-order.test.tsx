import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import Objects from "./Objects";
import { api } from "../api";

vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  api: vi.fn(),
}));

const call = vi.mocked(api);

/**
 * The list screen asks for a sort through the URL, but the server is the one
 * that decides which sort it could actually run: without `<type>.amount.read`
 * it drops `amount_desc` back to `updated_desc` so the ranking of a hidden
 * column cannot leak. Answer the list with whatever `order` the response
 * carries and let the screen read it.
 */
const answer = (listed: { order?: string }) => {
  call.mockImplementation(async (path: string) => {
    if (path.startsWith("/api/v1/suppliers")) return { items: [] } as never;
    if (path.startsWith("/api/v1/me/saved-views"))
      return { items: [], canShare: false } as never;
    return { items: [], count: 0, limit: 100, truncated: false, ...listed } as never;
  });
};

const show = (url: string) =>
  render(
    <MemoryRouter initialEntries={[url]}>
      <Objects type="contract" />
    </MemoryRouter>,
  );

const orderSelect = () => screen.getByLabelText("정렬 기준") as HTMLSelectElement;

describe("ObjectList 정렬", () => {
  // No beforeEach reset: vitest.config sets restoreMocks, and resetting a mock
  // that a previous test left a rejection in makes the runner report that
  // rejection against this file.

  it("says so when the server applied a different sort than the URL asked for", async () => {
    answer({ order: "updated_desc" });
    show("/contracts?order=amount_desc");

    await waitFor(() =>
      expect(document.body.textContent).toContain(
        "요청한 정렬을 적용할 수 없어 최근 수정순으로 표시했습니다",
      ),
    );
  });

  it("shows the applied sort in the dropdown, not the dropped one", async () => {
    // Leaving 금액 높은순 selected over rows that are in updated_at order makes
    // the top row read as the largest contract, with the amount column blank
    // because redactObject dropped it.
    answer({ order: "updated_desc" });
    show("/contracts?order=amount_desc");

    await waitFor(() =>
      expect(screen.queryByText(/계약 데이터가 없습니다/)).toBeTruthy(),
    );
    expect(orderSelect().value).toBe("updated_desc");
  });

  it("stays silent when the server applied the sort that was asked for", async () => {
    answer({ order: "due_asc" });
    show("/contracts?order=due_asc");

    await waitFor(() =>
      expect(screen.queryByText(/계약 데이터가 없습니다/)).toBeTruthy(),
    );
    expect(document.body.textContent).not.toContain("요청한 정렬을 적용할 수 없어");
    expect(orderSelect().value).toBe("due_asc");
  });

  it("leaves the dropdown on the URL value when the response carries no order", async () => {
    answer({});
    show("/contracts?order=amount_desc");

    await waitFor(() =>
      expect(screen.queryByText(/계약 데이터가 없습니다/)).toBeTruthy(),
    );
    expect(document.body.textContent).not.toContain("요청한 정렬을 적용할 수 없어");
    expect(orderSelect().value).toBe("amount_desc");
  });

  it("leaves the dropdown on the URL value when it cannot name the applied order", async () => {
    // A sort this dropdown has no option for cannot be announced by name, and
    // selecting it would blank the control rather than say anything.
    answer({ order: "cost_centre_asc" });
    show("/contracts?order=amount_desc");

    await waitFor(() =>
      expect(screen.queryByText(/계약 데이터가 없습니다/)).toBeTruthy(),
    );
    expect(document.body.textContent).not.toContain("요청한 정렬을 적용할 수 없어");
    expect(orderSelect().value).toBe("amount_desc");
  });
});
