import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import Objects from "./Objects";
import { api, APIError } from "../api";

vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  api: vi.fn(),
}));

const call = vi.mocked(api);

/**
 * Every work list in this product — 계약, 발주, 검수, Invoice, 지급 — renders this
 * one component, so a list request that is refused is refused on all of them at
 * once. The screen used to drop the rejection on the floor: `loading` is derived
 * from `result.key !== requestKey`, which a rejected request never satisfies, so
 * the spinner claimed "불러오는 중" for twelve seconds and then guessed at the
 * cause ("연결이 끊겼거나 서비스가 재시작 중일 수 있습니다") for a 403 the server
 * had already explained in words. These tests pin the screen to the server's own
 * reason and to a retry that re-sends the request rather than reloading the page.
 *
 * The list path is the only one that rejects: /api/v1/suppliers and
 * /api/v1/me/saved-views carry their own `.catch`, so they answer normally here.
 */
const refuse = (cause: unknown) => {
  call.mockImplementation(async (path: string) => {
    if (path.startsWith("/api/v1/suppliers")) return { items: [] } as never;
    if (path.startsWith("/api/v1/me/saved-views"))
      return { items: [], canShare: false } as never;
    throw cause;
  });
};

// Refuses the list until `heal()` is called, then answers it. The rejection is
// thrown inside the implementation rather than held in a variable, so no
// rejected promise is left floating for the runner to attribute to this file.
const refuseUntilHealed = (cause: unknown) => {
  let healed = false;
  call.mockImplementation(async (path: string) => {
    if (path.startsWith("/api/v1/suppliers")) return { items: [] } as never;
    if (path.startsWith("/api/v1/me/saved-views"))
      return { items: [], canShare: false } as never;
    if (!healed) throw cause;
    return { items: [], count: 0, limit: 100, truncated: false } as never;
  });
  return () => {
    healed = true;
  };
};

const show = (url: string) =>
  render(
    <MemoryRouter initialEntries={[url]}>
      <Objects type="contract" />
    </MemoryRouter>,
  );

const listCalls = () =>
  call.mock.calls.filter(([path]) => String(path).startsWith("/api/v1/contracts"));

describe("ObjectList 조회 실패", () => {
  // No beforeEach reset: vitest.config sets restoreMocks, and resetting a mock
  // that a previous test left a rejection in makes the runner report that
  // rejection against this file. Every test below waits for the screen to settle
  // so nothing is still in flight when it ends.

  it("shows the reason the server gave instead of a spinner", async () => {
    refuse(new APIError(403, "계약을 조회할 권한이 없습니다"));
    show("/contracts");

    await waitFor(() =>
      expect(
        screen.queryByText("계약을 조회할 권한이 없습니다"),
      ).toBeTruthy(),
    );
    expect(screen.queryByText("불러오는 중")).toBeNull();
  });

  it("retries the same request instead of reloading the page", async () => {
    const heal = refuseUntilHealed(new APIError(500, "요청 실패 (500)"));
    show("/contracts");

    await waitFor(() => expect(screen.queryByText("요청 실패 (500)")).toBeTruthy());
    const before = listCalls().length;

    heal();
    fireEvent.click(screen.getByRole("button", { name: /다시 시도/ }));

    await waitFor(() =>
      expect(screen.queryByText(/계약 데이터가 없습니다/)).toBeTruthy(),
    );
    expect(listCalls().length).toBe(before + 1);
  });

  it("drops the reason once a request succeeds", async () => {
    const heal = refuseUntilHealed(new APIError(500, "요청 실패 (500)"));
    show("/contracts");

    await waitFor(() => expect(screen.queryByText("요청 실패 (500)")).toBeTruthy());
    heal();
    fireEvent.click(screen.getByRole("button", { name: /다시 시도/ }));

    await waitFor(() =>
      expect(screen.queryByText(/계약 데이터가 없습니다/)).toBeTruthy(),
    );
    expect(document.body.textContent).not.toContain("요청 실패 (500)");
    expect(document.body.textContent).not.toContain("목록을 불러오지 못했습니다");
  });

  it("falls back to Korean when the rejection is not an Error", async () => {
    // A rejection that is not an Error, or an Error with an empty message, must
    // still end in words: an empty <Empty> description would say no more than
    // the spinner it replaced. The fallback is worded apart from the title so
    // the reader is not shown the same sentence twice.
    refuse("boom");
    show("/contracts");

    await waitFor(() =>
      expect(screen.queryByText("목록을 조회하지 못했습니다")).toBeTruthy(),
    );
    expect(screen.queryByText("불러오는 중")).toBeNull();
    expect(screen.queryByText("목록을 불러오지 못했습니다")).toBeTruthy();
  });

  it("falls back to Korean when the Error carries an empty message", async () => {
    refuse(new APIError(500, ""));
    show("/contracts");

    await waitFor(() =>
      expect(screen.queryByText("목록을 조회하지 못했습니다")).toBeTruthy(),
    );
    expect(screen.queryByText("불러오는 중")).toBeNull();
  });

  it("does not carry a stale failure onto the next filter", async () => {
    // The failure is stored against the requestKey it belongs to, so changing
    // the sort asks a new question and the old refusal stops applying to it
    // rather than hanging over the new request's spinner.
    const heal = refuseUntilHealed(new APIError(403, "계약을 조회할 권한이 없습니다"));
    show("/contracts?order=amount_desc");

    await waitFor(() =>
      expect(screen.queryByText("계약을 조회할 권한이 없습니다")).toBeTruthy(),
    );

    heal();
    fireEvent.change(screen.getByLabelText("정렬 기준"), {
      target: { value: "title_asc" },
    });

    await waitFor(() =>
      expect(screen.queryByText(/계약 데이터가 없습니다/)).toBeTruthy(),
    );
    expect(document.body.textContent).not.toContain("계약을 조회할 권한이 없습니다");
  });
});
