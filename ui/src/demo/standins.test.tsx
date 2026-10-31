import { expect, test, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import MonacoColorize from "./MonacoColorize";
import MonacoDiff from "./MonacoDiff";
import MonacoView from "./MonacoView";
import { QRCode } from "./QRCode";

// The demo's stand-ins for Monaco and the QR encoder show the same text,
// plain.

test("the editor shows its text, and takes typing when it is editable", () => {
  const { unmount } = render(<MonacoView text="[team]" language="ini" />);
  expect(screen.getByText("[team]").tagName).toBe("PRE");
  unmount();
  const onChange = vi.fn();
  render(<MonacoView text="draft" language="markdown" onChange={onChange} />);
  fireEvent.change(screen.getByDisplayValue("draft"), { target: { value: "draft 2" } });
  expect(onChange).toHaveBeenCalledWith("draft 2");
});

test("a changed file shows its last committed version beside the new one, and code stays plain", () => {
  render(
    <>
      <MonacoDiff path="main.go" now="package main" before="package old" />
      <MonacoColorize code="go test ./..." language="shell" />
    </>,
  );
  expect(screen.getByText("package main")).toBeTruthy();
  expect(screen.getByText("package old")).toBeTruthy();
  expect(screen.getByText("Last committed")).toBeTruthy();
  expect(screen.getByText("Now")).toBeTruthy();
  expect(screen.getByText("go test ./...")).toBeTruthy();
});

test("a file with nothing to compare shows as it is now", () => {
  render(<MonacoDiff path="main.go" now="package main" />);
  expect(screen.getByText("package main")).toBeTruthy();
  expect(screen.queryByText("Last committed")).toBeNull();
});

test("a QR code is a square with its label that says the demo has none", () => {
  render(<QRCode text="https://example.com" label="QR code of the sign-in page" className="size-28" />);
  const square = screen.getByRole("img", { name: "QR code of the sign-in page" });
  expect(square.className).toContain("size-28");
  expect(square.textContent).toBe("No QR code in the demo");
});
