import { expect, test } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";
import { addBook, fixture, kindleDocuments, notice, shelfBook } from "./helpers";

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

test("sends library books to the Kindle, starting from the Kindle page", async ({ page, request }) => {
  await addBook(request, "beloved.epub");
  await addBook(request, "skerries.epub");
  await page.goto("/kindle");
  await expect(page.getByLabel("Filter library books")).toHaveCount(0);

  await page.getByRole("link", { name: "Send books from the library" }).click();
  await expect(page).toHaveURL("/");
  const selected = page.getByRole("region", { name: "Selected books" });
  await expect(selected).toContainText("Tick the books to send to the Kindle");
  await selected.getByRole("button", { name: "Cancel" }).click();
  await expect(page).toHaveURL("/kindle");

  await page.getByRole("link", { name: "Send books from the library" }).click();
  await page.getByLabel("Search library").fill("beloved");
  await expect(shelfBook(page, "Letters from the Skerries", "Tove Aldane")).toHaveCount(0);
  await shelfBook(page, "Beloved", "Toni Morrison").click();
  await expect(page.getByLabel("Select Beloved")).toBeChecked();
  await expect(selected).toContainText("1 book selected");
  await selected.getByRole("button", { name: "Send to Kindle" }).click();
  await expect(notice(page, "Sent “Beloved” to the Kindle")).toBeVisible();

  await expect(page).toHaveURL("/kindle");
  const onKindle = page.getByRole("list", { name: "Books on the Kindle" });
  await expect(onKindle.getByRole("listitem").filter({ hasText: "Beloved" })).toBeVisible();

  await page.goto("/");
  await page.getByLabel("Search library").fill("o");
  await shelfBook(page, "Beloved", "Toni Morrison").hover();
  await page.getByLabel("Select Beloved").check();
  await shelfBook(page, "Letters from the Skerries", "Tove Aldane").click();
  await expect(selected).toContainText("2 books selected");
  await selected.getByRole("button", { name: "Send to Kindle" }).click();
  await expect(notice(page, "Sent “Letters from the Skerries” to the Kindle, skipped 1 already on it")).toBeVisible();
  await expect(selected).toHaveCount(0);
  await expect(page).toHaveURL(/\/\?q=o$/);
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
  await expect(page.getByRole("link", { name: "Books by Arkady Strugatsky" })).toBeVisible();
});
