import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import WorkInbox from "./WorkInbox";
import { api, APIError } from "../api";

vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  api: vi.fn(),
}));

const call = vi.mocked(api);

const show = () =>
  render(
    <MemoryRouter>
      <WorkInbox />
    </MemoryRouter>,
  );

/**
 * The control tower exists to say what is outstanding. When its one request
 * failed it used to render anyway with no data, so every figure fell back to
 * zero: "지금 먼저 볼 업무가 0건 있습니다", four zeroed signals, and an empty
 * state reading "모든 긴급 업무를 확인했습니다" — an all-clear nobody earned,
 * with the failure buried below it.
 */
describe("WorkInbox", () => {
  // No beforeEach reset: vitest.config sets restoreMocks, and resetting a mock
  // that a previous test left a rejection in makes the runner report that
  // rejection against this file.

  it("reports a failed load instead of announcing no work", async () => {
    call.mockImplementation(async () => {
      throw new APIError(503, "업무 신호를 읽지 못했습니다");
    });
    show();

    await waitFor(() =>
      expect(document.body.textContent).toContain("업무를 불러오지 못했습니다"),
    );
    expect(document.body.textContent).toContain("업무 신호를 읽지 못했습니다");
    expect(screen.getByRole("button", { name: /다시 시도/ })).toBeTruthy();
    // Nothing may claim there is no work, or that any of it was checked.
    expect(document.body.textContent).not.toContain("0건 있습니다");
    expect(document.body.textContent).not.toContain(
      "모든 긴급 업무를 확인했습니다",
    );
  });

  it("shows the work when the load succeeds", async () => {
    call.mockImplementation(
      async () =>
        ({
          items: [],
          summary: { critical: 4, overdue: 2, today: 1, total: 7 },
          categories: { all: 7 },
        }) as unknown as never,
    );
    show();

    await waitFor(() =>
      expect(document.body.textContent).toContain("4건 있습니다"),
    );
    expect(document.body.textContent).not.toContain(
      "업무를 불러오지 못했습니다",
    );
  });
});
