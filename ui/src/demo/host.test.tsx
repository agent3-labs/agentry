import { expect, test } from "vitest";
import { render, screen } from "@testing-library/react";
import { DOWNLOAD, Frame } from "./host";

test("the banner's link opens the download in a new tab, so a framed demo stays put", () => {
  render(<Frame>page</Frame>);
  const link = screen.getByRole("link", { name: "Download Agentry" });
  expect(link.getAttribute("href")).toBe(DOWNLOAD);
  expect(link.getAttribute("target")).toBe("_blank");
  expect(link.getAttribute("rel")).toBe("noopener noreferrer");
});
