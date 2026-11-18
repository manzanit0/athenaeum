import { expect, test } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";
import { addBook, fixture, kindleDocuments, notice } from "./helpers";

test("sends a book to the Kindle and removes it again", async ({ page, request }) => {
  const book = await addBook(request, "stoner.epub");
  const onDisk = path.join(kindleDocuments, "Stoner - John Williams.azw3");
  await page.goto(`/books/${book.id}`);

  await page.getByRole("button", { name: "Send to Kindle" }).click();
  await expect(notice(page, "Sent “Stoner” to the Kindle")).toBeVisible();
  expect(fs.existsSync(onDisk)).toBe(true);

  await page.goto("/kindle");
  const row = page.getByRole("list", { name: "Books on the Kindle" }).getByRole("listitem").filter({ hasText: "Stoner" });
  await expect(row.getByRole("link", { name: "In library" })).toBeVisible();

  await row.getByRole("button", { name: "Remove from Kindle" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Remove from Kindle" }).click();
  await expect(notice(page, "Removed “Stoner” from the Kindle")).toBeVisible();
  await expect(row).toHaveCount(0);
  expect(fs.existsSync(onDisk)).toBe(false);
});

test("adds a library book from the Kindle page", async ({ page, request }) => {
  await addBook(request, "beloved.epub");
  await page.goto("/kindle");

  const candidates = page.getByRole("list", { name: "Library books not on the Kindle" });
  await page.getByLabel("Filter library books").fill("beloved");
  await candidates.getByRole("button", { name: "Send to Kindle: Beloved" }).click();
  await expect(notice(page, "Sent “Beloved” to the Kindle")).toBeVisible();

  const onKindle = page.getByRole("list", { name: "Books on the Kindle" });
  await expect(onKindle.getByRole("listitem").filter({ hasText: "Beloved" })).toBeVisible();
  await expect(candidates.getByRole("listitem")).toHaveCount(0);
});

test("copies a book found on the Kindle into the library", async ({ page }) => {
  fs.copyFileSync(fixture("roadside.mobi"), path.join(kindleDocuments, "Roadside Picnic - Arkady Strugatsky.mobi"));
  await page.goto("/kindle");

  const row = page
    .getByRole("list", { name: "Books on the Kindle" })
    .getByRole("listitem")
    .filter({ hasText: "Roadside Picnic" });
  await row.getByRole("button", { name: "Copy to library" }).click();
  await expect(notice(page, "Copied “Roadside Picnic” to the library")).toBeVisible();

  await row.getByRole("link", { name: "In library" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Roadside Picnic" })).toBeVisible();
  await expect(page.getByLabel("Authors")).toHaveValue("Arkady Strugatsky");
});
