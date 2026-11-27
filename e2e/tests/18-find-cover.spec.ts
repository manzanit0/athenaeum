import { expect, test } from "@playwright/test";
import { addBook, notice } from "./helpers";

test("finds a cover online and uses it", async ({ page, request }) => {
  const book = await addBook(request, "kindred.epub");
  await page.goto(`/books/${book.id}/edit`);
  await page.getByRole("button", { name: "Find cover online" }).click();

  const dialog = page.getByRole("dialog", { name: "Find a cover" });
  await expect(dialog).toBeVisible();
  // The search starts from the book as it is filed, and can be changed.
  await expect(dialog.getByLabel("Title")).toHaveValue("Kindred");
  await expect(dialog.getByLabel("Author")).toHaveValue("Octavia E. Butler");

  // What was found is listed, each with where it came from and its size.
  await expect(dialog.getByRole("radio")).toHaveCount(3);
  await expect(dialog.getByRole("radio", { name: "Open Library · 200×300" })).toBeVisible();
  await expect(dialog.getByRole("radio", { name: "Open Library · 80×120" })).toBeVisible();
  await expect(dialog.getByRole("radio", { name: "Google Books · 400×600" })).toBeVisible();

  // Nothing is chosen until the reader says so.
  const use = dialog.getByRole("button", { name: "Use this cover" });
  await expect(use).toBeDisabled();
  await dialog.getByRole("radio", { name: "Open Library · 200×300" }).check();
  await expect(use).toBeEnabled();
  await use.click();

  await expect(notice(page, "Cover changed")).toBeVisible();
  await expect(dialog).toBeHidden();
  await expect(page.getByRole("button", { name: "Remove cover" })).toBeVisible();
  const cover = await request.get(`/api/books/${book.id}/cover`);
  expect(cover.status()).toBe(200);

  // The cover the book has now is shown beside the new candidates.
  await page.getByRole("button", { name: "Find cover online" }).click();
  await expect(dialog.getByText("Current cover")).toBeVisible();
});

test("says so when no cover is found or the websites cannot be reached", async ({ page, request }) => {
  const book = await addBook(request, "kindred.epub");
  const before = await request.get(`/api/books/${book.id}/cover`);
  await page.goto(`/books/${book.id}/edit`);
  await page.getByRole("button", { name: "Find cover online" }).click();
  const dialog = page.getByRole("dialog", { name: "Find a cover" });
  await expect(dialog.getByRole("radio")).toHaveCount(3);

  await dialog.getByLabel("Title").fill("Nothing");
  await dialog.getByRole("button", { name: "Search" }).click();
  await expect(dialog.getByText("No covers found")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Use this cover" })).toBeDisabled();

  await dialog.getByLabel("Title").fill("Unreachable");
  await dialog.getByRole("button", { name: "Search" }).click();
  await expect(dialog.getByRole("alert")).toContainText("Google Books (connection refused) and Open Library (connection refused) failed");

  // A new search drops what was picked before.
  await dialog.getByLabel("Title").fill("Kindred");
  await dialog.getByRole("button", { name: "Search" }).click();
  await expect(dialog.getByRole("radio")).toHaveCount(3);
  await expect(dialog.getByRole("button", { name: "Use this cover" })).toBeDisabled();

  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toBeHidden();
  // Cancelling leaves the cover as it was.
  const after = await request.get(`/api/books/${book.id}/cover`);
  expect(after.status()).toBe(before.status());
});
