import { expect, test } from "@playwright/test";
import { addBook, fixture, notice } from "./helpers";

test("converts a book, downloads the result and manages its files", async ({ page, request }) => {
  const book = await addBook(request, "piranesi.epub");
  await page.goto(`/books/${book.id}`);

  await page.getByLabel("Convert to").selectOption("azw3");
  await page.getByRole("button", { name: "Convert", exact: true }).click();
  await expect(notice(page, "Converted to AZW3")).toBeVisible();
  const converted = page.getByRole("listitem").filter({ hasText: "Piranesi - Susanna Clarke.azw3" });
  await expect(converted).toBeVisible();

  const download = page.waitForEvent("download");
  await page.getByRole("link", { name: "Download AZW3" }).click();
  expect((await download).suggestedFilename()).toBe("Piranesi - Susanna Clarke.azw3");

  await page.getByRole("button", { name: "Remove AZW3" }).click();
  await expect(notice(page, "Removed the AZW3 file")).toBeVisible();
  await expect(converted).toHaveCount(0);

  await page.getByLabel("Add a file").setInputFiles(fixture("roadside.mobi"));
  await expect(notice(page, "File added")).toBeVisible();
  await expect(page.getByRole("listitem").filter({ hasText: "Piranesi - Susanna Clarke.mobi" })).toBeVisible();
});
