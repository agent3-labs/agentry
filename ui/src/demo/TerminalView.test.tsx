import { expect, test } from "vitest";
import { render, screen } from "@testing-library/react";
import { TerminalView } from "./TerminalView";

test("the demo's terminal shows read-only lines, and nobody can take control", () => {
  render(<TerminalView url="ws://demo/api/tasks/task-12/terminal" title="engineer" />);
  expect(screen.getByText("engineer")).toBeTruthy();
  expect(screen.getByText(/Nothing runs in the demo/)).toBeTruthy();
  expect(screen.getByRole("button", { name: /Take control/ })).toHaveProperty("disabled", true);
});
