import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { UsersPanel } from "./Admin";
import { api, post } from "../api";
import { permissionCodes } from "../status";

vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  api: vi.fn(),
  post: vi.fn(),
}));

const call = vi.mocked(api);
const send = vi.mocked(post);

/**
 * The 권한 box is where a role's permissions are typed, and it had nothing
 * behind it — no list, and no way to see that a word opens no door. That is how
 * the catalogue the product ships with came to hold "procurement.*": accepted,
 * listed in the role table beside the ones that work, and matched by nothing,
 * so 구매 관리자 met a 403 on every procurement screen.
 */
describe("역할 권한", () => {
  function mount() {
    // The mocks are module-level: without this, a call another test made is
    // still in the list this one reads.
    vi.clearAllMocks();
    call.mockImplementation((path: string) => {
      if (path === "/api/v1/admin/roles") {
        return Promise.resolve({ items: [] }) as ReturnType<typeof api>;
      }
      return Promise.resolve({ items: [], truncated: false }) as ReturnType<
        typeof api
      >;
    });
    return render(<UsersPanel />);
  }

  async function openTheRoleForm() {
    mount();
    await screen.findByText("역할 · RBAC");
    fireEvent.click(screen.getByText("역할 · RBAC"));
    fireEvent.click(screen.getByText("역할 추가"));
    return await screen.findByText("사용자 역할 생성");
  }

  it("권한을 목록에서 고를 수 있다", async () => {
    send.mockResolvedValue({ id: "r1" });
    await openTheRoleForm();

    // Every permission the API checks is offered; the writer no longer has to
    // remember the spelling of a word that fails silently.
    // Wildcards stay a thing the box takes rather than a chip: the picker
    // offers the doors themselves.
    for (const code of ["dashboard.read", "contract.read", "supplier.read"]) {
      expect(permissionCodes).toContain(code);
    }
    const box = screen.getByPlaceholderText(
      /supplier.read/,
    ) as HTMLTextAreaElement;
    fireEvent.click(screen.getByRole("button", { name: "dashboard.read" }));
    fireEvent.click(screen.getByRole("button", { name: "contract.read" }));
    expect(box.value.split("\n")).toEqual(["dashboard.read", "contract.read"]);
    // And a permission picked by mistake comes back off.
    fireEvent.click(screen.getByRole("button", { name: "contract.read" }));
    expect(box.value.split("\n")).toEqual(["dashboard.read"]);

    fireEvent.change(screen.getByLabelText(/역할 이름/), {
      target: { value: "구매 검토자" },
    });
    fireEvent.change(screen.getByLabelText(/역할 코드/), {
      target: { value: "review" },
    });
    fireEvent.submit(box.closest("form")!);
    await waitFor(() => expect(send).toHaveBeenCalled());
    expect(send.mock.calls[0][1]).toMatchObject({
      code: "review",
      permissions: ["dashboard.read"],
    });
  });

  it("API가 거절한 권한을 폼에서 말한다", async () => {
    send.mockRejectedValue(
      new Error(
        '권한 "procurement.*"는 이 시스템이 확인하지 않는 권한입니다. 사용할 수 있는 권한 영역은 ai, analytics입니다',
      ),
    );
    await openTheRoleForm();
    const box = screen.getByPlaceholderText(
      /supplier.read/,
    ) as HTMLTextAreaElement;
    fireEvent.change(box, { target: { value: "procurement.*" } });
    fireEvent.change(screen.getByLabelText(/역할 이름/), {
      target: { value: "구매 관리자" },
    });
    fireEvent.change(screen.getByLabelText(/역할 코드/), {
      target: { value: "pm" },
    });
    fireEvent.submit(box.closest("form")!);

    // The refusal used to be thrown out of the submit handler: the modal stayed
    // open with no word said, which reads as a save that worked.
    expect(await screen.findByRole("alert")).toHaveTextContent("procurement.*");
    expect(screen.getByText("사용자 역할 생성")).toBeInTheDocument();
  });
});
