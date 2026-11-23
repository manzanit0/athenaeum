import { expect, test } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";
import { fixture, kindleDocuments, notice, shelfBook } from "./helpers";

test("imports a Word document, reads it and sends it to the Kindle", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Add books").setInputFiles(fixture("logbook.docx"));
  await expect(notice(page, "Added 1 book")).toBeVisible();

  await shelfBook(page, "The Harbour Logbook", "Edda Voss").click();
  await expect(page.getByRole("heading", { level: 1, name: "The Harbour Logbook" })).toBeVisible();
  await expect(page.getByText("A season of arrivals and departures.")).toBeVisible();
  await expect(page.getByRole("link", { name: "Books in Harbours" })).toBeVisible();
  await expect(page.getByText(/Pages app/)).toHaveCount(0);

  await page.getByRole("link", { name: /^Read / }).click();
  const pages = page.frameLocator('iframe[title="The Harbour Logbook"]');
  await expect(pages.getByRole("heading", { name: "Spring Tides" })).toBeVisible();
  await expect(pages.getByText("It was a quiet morning in the library.")).toBeVisible();
  await expect(page.getByText("Section 1 of 3")).toBeVisible();
  await page.getByLabel("Contents").selectOption({ label: "Storm Surge" });
  await expect(pages.getByRole("heading", { name: "Storm Surge" })).toBeVisible();

  await page.getByRole("link", { name: "Back to the book" }).click();
  await page.getByRole("button", { name: "Send to Kindle" }).click();
  await expect(notice(page, "Sent “The Harbour Logbook” to the Kindle")).toBeVisible();
  expect(fs.existsSync(path.join(kindleDocuments, "The Harbour Logbook - Edda Voss.azw3"))).toBe(true);
  await expect(page.getByRole("link", { name: "Download AZW3" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Download DOCX" })).toBeVisible();
});

test("keeps a Pages document and says to convert it in the Pages app", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Add books").setInputFiles(fixture("Shore Sketches - Edda Voss.pages"));
  await expect(notice(page, "Added 1 book")).toBeVisible();

  await shelfBook(page, "Shore Sketches", "Edda Voss").click();
  await expect(page.getByRole("heading", { level: 1, name: "Shore Sketches" })).toBeVisible();
  const book = page.url();
  const hint = page.getByText("open it in the Pages app, export it as EPUB or Word, and add that file to this book");
  await expect(hint).toBeVisible();
  await expect(page.getByRole("link", { name: /^Read / })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Send to Kindle" })).toBeDisabled();
  const download = page.waitForEvent("download");
  await page.getByRole("link", { name: "Download PAGES" }).click();
  expect((await download).suggestedFilename()).toBe("Shore Sketches - Edda Voss.pages");

  await page.goto(`${book}/preview`);
  await expect(page.getByText(/open the book in the Pages app, export it as EPUB or Word/)).toBeVisible();

  await page.goto(`${book}/edit`);
  await expect(hint).toBeVisible();
  await expect(page.getByRole("button", { name: "Remove cover" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Convert", exact: true })).toBeDisabled();
  await page.getByLabel("Add a file").setInputFiles(fixture("logbook.docx"));
  await expect(notice(page, "File added")).toBeVisible();
  await expect(page.getByRole("button", { name: "Convert", exact: true })).toBeEnabled();
  await page.getByRole("link", { name: "Done" }).click();
  await expect(page.getByRole("link", { name: /^Read / })).toBeVisible();
  await expect(hint).toBeVisible();
});
