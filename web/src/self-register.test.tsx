import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SupplierRegistration } from "./App";
import { APIError, api, post } from "./api";

// `post` closes over `api` in the real module, so mocking `api` alone would let
// a submit go straight out to fetch. Both wrappers this page uses are mocked.
vi.mock("./api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./api")>()),
  api: vi.fn(),
  post: vi.fn(),
}));

const version = vi.mocked(api);
const register = vi.mocked(post);

// The signup transaction can hit two different unique keys — the company's
// 사업자번호 and the account's email — and both used to come back as the same
// "가입을 완료하지 못했습니다". Neither is escapable from this form: the
// 사업자번호 is correct, and the email is not even on the form, it rides on the
// invitation. So the page has to say which one it was, and for the address it
// has to offer the sign-in, because there is nothing here left to try.
describe("the 공급업체 계정 등록 refusals", () => {
  // Braces on purpose: the chain returns the mock itself, and a beforeEach that
  // returns a function hands Vitest that function as the teardown hook, which
  // would then call the mock again after the test.
  beforeEach(() => {
    window.history.replaceState({}, "", "/register?token=invitation-token");
    version.mockReset().mockResolvedValue({});
    register.mockReset().mockResolvedValue({});
  });

  function submit() {
    fireEvent.submit(screen.getByLabelText("담당자 이름").closest("form")!);
  }

  it("offers the sign-in when the address already has an account", async () => {
    render(<SupplierRegistration />);
    register.mockRejectedValue(
      new APIError(
        409,
        "이미 가입된 이메일입니다. 기존 계정으로 로그인하세요",
        "email_registered",
      ),
    );
    submit();
    expect(
      await screen.findByText(/이미 가입된 이메일입니다/),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Vendra 로그인" })).toHaveAttribute(
      "href",
      "/",
    );
  });

  it("names the company already on file without offering the sign-in", async () => {
    render(<SupplierRegistration />);
    register.mockRejectedValue(
      new APIError(
        409,
        "이미 등록된 사업자번호입니다. 담당 구매 담당자에게 기존 업체 계정으로 초대를 요청하세요",
        "duplicate_business_number",
      ),
    );
    submit();
    expect(
      await screen.findByText(/이미 등록된 사업자번호입니다/),
    ).toBeInTheDocument();
    // This person has no account yet, so a login link would be a dead end.
    expect(
      screen.queryByRole("link", { name: "Vendra 로그인" }),
    ).not.toBeInTheDocument();
  });
});
