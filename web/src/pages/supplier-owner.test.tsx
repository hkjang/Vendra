import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SupplierEdit } from "./Suppliers";
import { api, patch } from "../api";
import { Supplier } from "../types";

vi.mock("../api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api")>()),
  api: vi.fn(),
  patch: vi.fn(),
}));

const load = vi.mocked(api);
const save = vi.mocked(patch);

const departed = "22222222-2222-2222-2222-222222222222";
const successor = "33333333-3333-3333-3333-333333333333";
const theirTeam = "44444444-4444-4444-4444-444444444444";

function supplier(overrides: Partial<Supplier> = {}): Supplier {
  return {
    id: "11111111-1111-1111-1111-111111111111",
    supplierNumber: "SUP-0001",
    name: "한빛정밀",
    businessNumber: "123-45-67891",
    status: "registration",
    riskLevel: "LOW",
    categories: [],
    addresses: [],
    financials: {},
    taxInfo: {},
    metadata: {},
    annualSpend: 0,
    createdAt: "2026-01-01T00:00:00+09:00",
    updatedAt: "2026-01-01T00:00:00+09:00",
    ...overrides,
  } as Supplier;
}

// The 담당자 was settled at registration and never again — the edit form had no
// box for it and the API dropped the field if a client sent one. A supplier
// whose owner left the company stayed on their name, and one the portal
// registered itself has no owner at all; for an account whose data scope is
// 'own' that means the record is invisible to the person working with it.
describe("the 공급업체 Master 편집 담당자", () => {
  // Braces on purpose: the chain returns the mock itself, and a beforeEach that
  // returns a function hands Vitest that function as the teardown hook.
  beforeEach(() => {
    save.mockReset().mockResolvedValue({});
    load.mockReset().mockResolvedValue({
      items: [
        {
          id: successor,
          displayName: "후임 담당자",
          email: "successor@vendra.test",
          organizationId: theirTeam,
          organizationName: "구매2팀",
        },
      ],
    });
  });

  it("says so when nobody is on the record", async () => {
    render(
      <SupplierEdit
        supplier={supplier()}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    expect(
      await screen.findByText("담당자가 지정되어 있지 않습니다."),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/^담당자/)).toHaveValue("");
  });

  // The case the register cannot describe on its own: the owner is a real id
  // the picker will never offer, so the form has to name the situation rather
  // than show an empty box.
  it("says so when the owner is no longer somebody who can hold the record", async () => {
    render(
      <SupplierEdit
        supplier={supplier({ ownerId: departed })}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    expect(
      await screen.findByText(
        "현재 담당자는 조직 범위 밖이거나 비활성 계정입니다.",
      ),
    ).toBeInTheDocument();
  });

  it("hands the supplier to the chosen 담당자 along with their 조직", async () => {
    render(
      <SupplierEdit
        supplier={supplier()}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    await screen.findByRole("option", { name: /후임 담당자/ });
    await userEvent.selectOptions(screen.getByLabelText(/^담당자/), successor);
    expect(screen.getByRole("checkbox", { name: /구매2팀/ })).toBeChecked();
    await userEvent.click(screen.getByRole("button", { name: "Master 저장" }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0][1]).toMatchObject({
      ownerId: successor,
      organizationId: theirTeam,
    });
  });

  // Handing the record on and moving it between departments are two different
  // scopes, so the move is offered rather than assumed.
  it("leaves the 조직 alone when the transfer is declined", async () => {
    render(
      <SupplierEdit
        supplier={supplier()}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    await screen.findByRole("option", { name: /후임 담당자/ });
    await userEvent.selectOptions(screen.getByLabelText(/^담당자/), successor);
    await userEvent.click(screen.getByRole("checkbox", { name: /구매2팀/ }));
    await userEvent.click(screen.getByRole("button", { name: "Master 저장" }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0][1]).toMatchObject({
      ownerId: successor,
      organizationId: "",
    });
  });

  // An edit that is not a transfer must not move the record: the API keeps the
  // owner it has when the field arrives empty.
  it("keeps the current owner when the box is not touched", async () => {
    render(
      <SupplierEdit
        supplier={supplier({ ownerId: successor, organizationId: theirTeam })}
        onClose={() => {}}
        onSaved={() => {}}
      />,
    );
    await screen.findByText("현재 담당자 후임 담당자");
    expect(screen.queryByRole("checkbox", { name: /구매2팀/ })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Master 저장" }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(save.mock.calls[0][1]).toMatchObject({
      ownerId: successor,
      organizationId: "",
    });
  });
});
