import { expect, test } from "@playwright/test";
import { addBook, notice, shelfBook } from "./helpers";

test("deletes a book from the library", async ({ page, request }) => {
  const book = await addBook(request, "handbook.pdf");
  await page.goto(`/books/${book.id}`);
  await expect(page.getByRole("button", { name: "Delete book" })).toHaveCount(0);
  await page.getByRole("link", { name: "Edit book" }).click();

  await page.getByRole("button", { name: "Delete book" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("Delete “Typography Handbook”?");
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toBeHidden();

  await page.getByRole("button", { name: "Delete book" }).click();
  await dialog.getByRole("button", { name: "Delete book" }).click();
  await expect(notice(page, "Deleted “Typography Handbook”")).toBeVisible();
  await expect(page).toHaveURL("/");

  const response = await request.get(`/api/books/${book.id}`);
  expect(response.status()).toBe(404);
});

test("shows finished work in the activity log", async ({ page, request }) => {
  const book = await addBook(request, "kindred.epub");
  await page.goto(`/books/${book.id}/edit`);
  await page.getByLabel("Convert to").selectOption("mobi");
  await page.getByRole("button", { name: "Convert", exact: true }).click();
  await expect(notice(page, "Converted to MOBI")).toBeVisible();

  await page.goto("/activity");
  const entry = page.getByRole("listitem").filter({ hasText: 'Convert "Kindred" from EPUB to MOBI' }).first();
  await expect(entry).toContainText("Done");
  await entry.getByRole("link").click();
  await expect(page.getByRole("heading", { level: 1, name: "Kindred" })).toBeVisible();
  await expect(shelfBook(page, "Kindred", "Octavia E. Butler")).toHaveCount(0);

  await page.goto("/activity");
  await expect(page.getByText(/^Athenaeum version \S+/)).toBeVisible();
  await expect(page.getByText(/^Version /)).toHaveCount(0);
});
