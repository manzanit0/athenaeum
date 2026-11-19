import { expect, test } from "@playwright/test";
import { addBook, notice } from "./helpers";

test("turns a PDF into an EPUB and an AZW3 and reads the result", async ({ page, request }) => {
  const book = await addBook(request, "tide-tables.pdf");
  await page.goto(`/books/${book.id}/edit`);

  await page.getByLabel("Convert to").selectOption("epub");
  await page.getByRole("button", { name: "Convert", exact: true }).click();
  await expect(notice(page, "Converted to EPUB")).toBeVisible();
  await expect(page.getByRole("listitem").filter({ hasText: "Tide Tables - Ines Marlow.epub" })).toBeVisible();

  await page.getByLabel("Convert to").selectOption("azw3");
  await page.getByRole("button", { name: "Convert", exact: true }).click();
  await expect(notice(page, "Converted to AZW3")).toBeVisible();
  const kindle = await request.get(`/api/books/${book.id}/formats/azw3`);
  expect((await kindle.body()).subarray(60, 68).toString()).toBe("BOOKMOBI");

  await page.getByRole("link", { name: /^Read / }).click();
  await expect(page.getByLabel("File")).toHaveValue("epub");
  const pages = page.frameLocator('iframe[title="Tide Tables"]');
  await expect(pages.getByRole("heading", { name: "Tide Tables" })).toBeVisible();
  await page.getByRole("button", { name: "Next" }).click();
  await expect(pages.getByRole("heading", { name: "Spring Tides" })).toBeVisible();
  await expect(pages.getByText("It was a quiet morning in the library, and the tide tables lay open on the long desk")).toBeVisible();
  await page.getByLabel("Contents").selectOption({ label: "Storm Surge" });
  await expect(pages.getByRole("heading", { name: "Storm Surge" })).toBeVisible();
});

test("says why a scanned PDF cannot be converted", async ({ page, request }) => {
  const book = await addBook(request, "handbook.pdf");
  await page.goto(`/books/${book.id}/edit`);
  await page.getByLabel("Convert to").selectOption("epub");
  await page.getByRole("button", { name: "Convert", exact: true }).click();
  await expect(page.getByRole("alert").filter({ hasText: "its pages look like scans" })).toBeVisible();
  await expect(page.getByRole("listitem").filter({ hasText: ".epub" })).toHaveCount(0);

  await page.goto("/activity");
  await expect(page.getByText("recognising text in images is not supported").first()).toBeVisible();
});
