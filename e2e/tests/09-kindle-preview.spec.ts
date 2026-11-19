import { expect, test } from "@playwright/test";
import { addBook, notice } from "./helpers";

test("reads a Kindle book made from an EPUB and converts it back", async ({ page, request }) => {
  const book = await addBook(request, "almanac.epub");
  await page.goto(`/books/${book.id}/edit`);
  await page.getByLabel("Convert to").selectOption("azw3");
  await page.getByRole("button", { name: "Convert", exact: true }).click();
  await expect(notice(page, "Converted to AZW3")).toBeVisible();
  await page.getByRole("button", { name: "Remove EPUB" }).click();
  await expect(notice(page, "Removed the EPUB file")).toBeVisible();

  await page.getByRole("link", { name: /^Read / }).click();
  const pages = page.frameLocator('iframe[title="The Tidewater Almanac"]');
  await expect(pages.getByRole("heading", { name: "Spring Tides" })).toBeVisible();
  await expect(page.getByText("Section 1 of 3")).toBeVisible();
  await page.getByLabel("Contents").selectOption({ label: "Storm Surge" });
  await expect(pages.getByRole("heading", { name: "Storm Surge" })).toBeVisible();

  await page.getByRole("link", { name: "Back to the book" }).click();
  await page.getByRole("link", { name: "Edit book" }).click();
  await page.getByLabel("Convert to").selectOption("epub");
  await page.getByRole("button", { name: "Convert", exact: true }).click();
  await expect(notice(page, "Converted to EPUB")).toBeVisible();
  await expect(page.getByRole("listitem").filter({ hasText: /The Tidewater Almanac - Ines Marlow( \(\d+\))?\.epub/ })).toBeVisible();
});
