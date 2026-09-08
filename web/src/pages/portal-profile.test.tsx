import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Portal from "./Portal";
import { ToastProvider } from "../feedback";
import { api, APIError, patch } from "../api";
import type { Principal, Version } from "../api";

vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  api: vi.fn(),
  patch: vi.fn(),
}));

const call = vi.mocked(api);
const save = vi.mocked(patch);

const user: Principal = {
  id: "u1",
  email: "bidder@vendor.test",
  displayName: "포털 담당자",
  userType: "supplier",
  supplierId: "s1",
  permissions: ["portal.*"],
  dataScope: "own",
};
const version: Version = {
  name: "Vendra",
  version: "test",
  commit: "test",
  buildTime: "test",
};

// The register is full of addresses in this shape: the buyer's edit form takes
// any text, and this is how a website is printed on a business card.
const supplier = {
  id: "s1",
  name: "응찰 업체",
  supplierNumber: "SUP-1",
  businessNumber: "123-45-67890",
  status: "active",
  phone: "02-1234-5678",
  email: "sales@bidder.test",
  website: "www.bidder.test",
};

function show() {
  window.location.hash = "#profile";
  return render(
    <MemoryRouter>
      <ToastProvider>
        <Portal user={user} version={version} onLogout={() => {}} />
      </ToastProvider>
    </MemoryRouter>,
  );
}

async function openTheEditor() {
  show();
  fireEvent.click(await screen.findByRole("button", { name: /수정/ }));
  return (await screen.findByLabelText("웹사이트")) as HTMLInputElement;
}

/**
 * 회사 연락정보 수정 declared type="url" on the website box while the buyer's
 * own form, writing the same column, took any text. So the value the supplier
 * was shown was one their browser would not let them submit: opening the modal
 * to correct a telephone number ended at a validation bubble on a field they
 * had never touched, with no way past it except editing somebody else's value,
 * and nothing on the screen explaining any of it.
 */
describe("the portal can correct its own contact details", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    save.mockResolvedValue(undefined as never);
    call.mockImplementation(async (path: string) => {
      if (path.includes("/portal/profile")) return { supplier, user } as never;
      return { items: [] } as never;
    });
  });

  it("does not hold the website box to a shape the register does not store", async () => {
    const website = await openTheEditor();
    expect(website).toHaveValue("www.bidder.test");
    // The browser's own verdict on the value the buyer wrote. type="url" made
    // this false, and a false here is a form that will not submit.
    expect(website.checkValidity()).toBe(true);
  });

  it("saves the number with the address the buyer wrote", async () => {
    const website = await openTheEditor();
    fireEvent.change(screen.getByLabelText("대표 전화"), {
      target: { value: "031-987-6543" },
    });
    fireEvent.submit(website.closest("form")!);

    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0][0]).toBe("/api/v1/portal/profile");
    expect(save.mock.calls[0][1]).toEqual({
      email: "sales@bidder.test",
      phone: "031-987-6543",
      website: "www.bidder.test",
    });
  });

  it("says why a refused save did not happen", async () => {
    const website = await openTheEditor();
    save.mockRejectedValue(
      new APIError(400, "웹사이트는 올바른 주소 형식이 아닙니다"),
    );
    fireEvent.change(website, { target: { value: "사내 위키" } });
    fireEvent.submit(website.closest("form")!);

    // The rejection used to throw out of the submit handler: the modal stayed
    // open with the values still in it and nothing said the save had failed.
    expect(
      await screen.findByText("웹사이트는 올바른 주소 형식이 아닙니다"),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("웹사이트")).toHaveValue("사내 위키");
  });
});
