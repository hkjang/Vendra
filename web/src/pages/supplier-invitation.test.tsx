import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SupplierInvitation } from "./Suppliers";
import { api, del, post } from "../api";
import { Supplier } from "../types";

// The page's wrappers close over the real `api`, so mocking `api` alone would
// leave post and del hitting fetch.
vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  api: vi.fn(),
  post: vi.fn(),
  del: vi.fn(),
}));

const load = vi.mocked(api);
const issue = vi.mocked(post);
const recall = vi.mocked(del);

const supplier = {
  id: "22222222-2222-2222-2222-222222222222",
  supplierNumber: "SUP-0002",
  name: "한빛정밀",
  email: "sales@hanbit.test",
  status: "active",
} as Supplier;

function invitation(email: string, status: string) {
  return {
    id: `id-${email}`,
    email,
    status,
    createdAt: "2026-09-01T00:00:00+09:00",
    expiresAt: "2026-09-08T00:00:00+09:00",
  };
}

describe("the Supplier Portal 초대 modal", () => {
  beforeEach(() => {
    // Braces: Vitest treats a value returned from beforeEach as a teardown
    // hook, and this chain returns the mock itself.
    load.mockReset();
    issue.mockReset().mockResolvedValue({ invitationUrl: "/register?token=t" });
    recall.mockReset().mockResolvedValue(undefined);
  });

  // Issuing the link was the whole of the feature: it was handed over as a
  // one-time URL and after the modal closed nothing said who had been invited
  // or whether they had used it. The link is a bearer credential — whoever
  // holds it signs up as this company — so "did that go to the old address?"
  // was a question with no answer anywhere in the application.
  it("shows what has been issued and what became of it", async () => {
    load.mockResolvedValue({
      items: [
        invitation("gone@hanbit.test", "pending"),
        invitation("used@hanbit.test", "accepted"),
        invitation("old@hanbit.test", "expired"),
      ],
    });
    render(<SupplierInvitation supplier={supplier} onClose={() => {}} />);
    await screen.findByText("gone@hanbit.test");
    expect(load).toHaveBeenCalledWith(
      `/api/v1/invitations?supplierId=${supplier.id}`,
    );
    expect(screen.getByText("유효")).toBeInTheDocument();
    expect(screen.getByText("가입 완료")).toBeInTheDocument();
    expect(screen.getByText("기간 만료")).toBeInTheDocument();
    // Only the live one can be called back: the others have already stopped
    // working, and an 회수 button on the accepted one would promise to undo an
    // account that already exists.
    expect(screen.getAllByRole("button", { name: "회수" })).toHaveLength(1);
    const live = screen.getByText("gone@hanbit.test").closest("tr")!;
    expect(
      within(live).getByRole("button", { name: "회수" }),
    ).toBeInTheDocument();
  });

  it("calls the link back and reads the list again", async () => {
    load.mockResolvedValue({
      items: [invitation("gone@hanbit.test", "pending")],
    });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    render(<SupplierInvitation supplier={supplier} onClose={() => {}} />);
    await screen.findByText("gone@hanbit.test");
    load.mockResolvedValue({
      items: [invitation("gone@hanbit.test", "revoked")],
    });

    await userEvent.click(screen.getByRole("button", { name: "회수" }));
    await waitFor(() =>
      expect(recall).toHaveBeenCalledWith(
        "/api/v1/invitations/id-gone@hanbit.test",
      ),
    );
    // Recalling is not undoable and the address is the only thing that says
    // which link is being stopped, so it is in the question.
    expect(confirm.mock.calls[0][0]).toContain("gone@hanbit.test");
    expect(await screen.findByText("회수됨")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "회수" })).toBeNull();
    confirm.mockRestore();
  });

  // A redeemed invitation cannot be recalled, and the API says so with a 409.
  // Without this the refusal went nowhere: the row would keep its 회수 button
  // and pressing it would look like nothing happened at all.
  it("says why a recall was refused", async () => {
    load.mockResolvedValue({
      items: [invitation("used@hanbit.test", "pending")],
    });
    recall.mockRejectedValue(new Error("이미 가입에 사용된 초대입니다"));
    vi.spyOn(window, "confirm").mockReturnValue(true);
    render(<SupplierInvitation supplier={supplier} onClose={() => {}} />);
    await screen.findByText("used@hanbit.test");

    await userEvent.click(screen.getByRole("button", { name: "회수" }));
    expect(
      await screen.findByText("이미 가입에 사용된 초대입니다"),
    ).toBeInTheDocument();
  });

  // The list is context for the form, not the point of the modal.
  it("still issues a link when the list cannot be read", async () => {
    load.mockRejectedValue(new Error("초대 목록을 조회하지 못했습니다"));
    render(<SupplierInvitation supplier={supplier} onClose={() => {}} />);
    await userEvent.click(
      screen.getByRole("button", { name: "초대 링크 발급" }),
    );
    await waitFor(() => expect(issue).toHaveBeenCalled());
    expect(issue.mock.calls[0][1]).toMatchObject({
      email: supplier.email,
      supplierId: supplier.id,
    });
  });
});
