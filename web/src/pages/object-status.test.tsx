import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ObjectTable } from "./Objects";
import { objectStatusFilters, submittableStatuses } from "../status";
import { post } from "../api";
import { BusinessObject } from "../types";

vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  post: vi.fn(),
}));

const send = vi.mocked(post);

function contract(status: string): BusinessObject {
  return {
    id: "22222222-2222-2222-2222-222222222222",
    objectType: "contract",
    number: "CTR-0001",
    title: "정밀가공 단가계약",
    status,
    currency: "KRW",
    data: {},
    createdAt: "2026-01-01T00:00:00+09:00",
    updatedAt: "2026-01-01T00:00:00+09:00",
  };
}

function table(status: string) {
  return render(
    <ObjectTable
      items={[contract(status)]}
      empty="등록된 계약이 없습니다"
      onSubmit={() => {}}
      visibleColumns={["status"]}
    />,
  );
}

describe("a request an approver handed back", () => {
  beforeEach(() => send.mockReset().mockResolvedValue({}));

  // 보완 요청 is the decision that exists so a request can be fixed and sent
  // again; it is the only thing that separates it from 반려. The list offered
  // 승인 요청 on "draft" alone, so the request came back and stopped there —
  // no button, a disabled checkbox, and no way to file it again short of
  // typing the whole contract in as a new one.
  it("can be sent for approval again", async () => {
    table("returned");
    await userEvent.click(
      screen.getByRole("button", { name: "보완 후 다시 승인 요청" }),
    );
    await waitFor(() => expect(send).toHaveBeenCalled());
    expect(send.mock.calls[0][0]).toBe(
      "/api/v1/contracts/22222222-2222-2222-2222-222222222222/submit",
    );
  });

  it("can be picked up by the bulk 승인 요청", async () => {
    table("returned");
    const box = screen.getByRole("checkbox", { name: "정밀가공 단가계약 선택" });
    expect(box).toBeEnabled();
    await userEvent.click(box);
    await userEvent.click(screen.getByRole("button", { name: /일괄 승인 요청/ }));
    await waitFor(() => expect(send).toHaveBeenCalled());
    expect(send.mock.calls[0][0]).toBe(
      "/api/v1/contracts/22222222-2222-2222-2222-222222222222/submit",
    );
  });

  // And it reads as something waiting on the author rather than as the raw
  // English word every unrecognised status used to show.
  it("says so in Korean", () => {
    table("returned");
    expect(screen.getByText("보완 요청")).toBeInTheDocument();
    expect(screen.queryByText("returned")).not.toBeInTheDocument();
  });

  // The filter is the only way to find one, and it had no option for them.
  it("can be filtered for", () => {
    expect(objectStatusFilters.map((s) => s.value)).toContain("returned");
  });
});

describe("the 승인 요청 button", () => {
  beforeEach(() => send.mockReset().mockResolvedValue({}));

  it("is offered on a draft", () => {
    table("draft");
    expect(
      screen.getByRole("button", { name: "승인 요청" }),
    ).toBeInTheDocument();
  });

  // A request already with the approvers, or already decided, is not one to
  // file again: submitting it would either be refused as already submitted or
  // start a second approval for something that has been settled.
  it("is withheld from a status the workflow owns", () => {
    for (const status of ["pending_approval", "approved", "rejected"]) {
      expect(submittableStatuses).not.toContain(status);
    }
    for (const status of ["pending_approval", "approved", "rejected"]) {
      const { unmount } = table(status);
      expect(screen.queryByRole("button", { name: /승인 요청/ })).toBeNull();
      expect(
        screen.getByRole("checkbox", { name: "정밀가공 단가계약 선택" }),
      ).toBeDisabled();
      unmount();
    }
  });
});
