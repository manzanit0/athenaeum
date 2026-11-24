import { expect, test } from "@playwright/test";
import fs from "node:fs";
import { addBook, modelDecline, notice } from "./helpers";

test.afterEach(() => {
  fs.rmSync(modelDecline, { force: true });
});

test("restores a scanned PDF into an EPUB from start to end", async ({ page, request }) => {
  const book = await addBook(request, "soundings.pdf");
  await page.goto(`/books/${book.id}/edit`);

  // The need shows up where a conversion fails, and the way out is offered there.
  await page.getByLabel("Convert to").selectOption("epub");
  await page.getByRole("button", { name: "Convert", exact: true }).click();
  const offer = page.getByRole("alert").filter({ hasText: "This PDF could not be converted: its pages are scans." });
  await offer.getByRole("link", { name: "Restore it into text" }).click();
  await expect(page).toHaveURL(`/books/${book.id}/restore`);
  await expect(page.getByRole("heading", { name: "Restore “Soundings”" })).toBeVisible();

  // What it would do and cost, before anything is sent.
  await expect(page.getByText("30 pages, and 2 pages that already have text.")).toBeVisible();
  await expect(page.getByText("Pages to send").locator("xpath=following-sibling::dd")).toHaveText("32");
  await expect(page.getByText("Estimated cost").locator("xpath=following-sibling::dd")).toHaveText("$0.63 to $1.87");
  await expect(page.getByText("It reads the first 10 pages and ends there")).toBeVisible();
  await expect(page.getByText("The pages are sent to Anthropic to be read. The PDF is kept as it is.")).toBeVisible();

  // It needs a key once.
  const start = page.getByRole("button", { name: "Restore the book" });
  await expect(start).toBeDisabled();
  await page.getByLabel("Anthropic API key").fill("sk-ant-test");
  await page.getByRole("button", { name: "Save the key" }).click();
  await expect(notice(page, "Key saved")).toBeVisible();
  await expect(page.getByLabel("Anthropic API key")).toHaveCount(0);

  await start.click();
  await expect(page.getByRole("heading", { name: "The book has been restored" })).toBeVisible();
  await expect(page.getByText("Pages read").locator("xpath=following-sibling::dd")).toHaveText("32");
  await expect(page.getByText("Chapters", { exact: true }).locator("xpath=following-sibling::dd")).toHaveText("32");
  await expect(page.getByText("Notes linked").locator("xpath=following-sibling::dd")).toHaveText("30");
  const doubts = page.getByRole("list", { name: "Passages to check" }).getByRole("listitem");
  await expect(doubts).toHaveCount(8);
  await expect(doubts.first()).toContainText("Page 1");
  await expect(doubts.first()).toContainText("The word is smudged.");
  await page.getByRole("button", { name: "Show all 30" }).click();
  await expect(doubts).toHaveCount(30);

  // Restoring again would replace what is there now, and says so.
  await expect(page.getByText("This book already has an EPUB. The restored one takes its place.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Restore and replace the EPUB" })).toBeEnabled();

  // The restored book reads as a book: headings in one style, notes linked.
  await page.getByRole("link", { name: "Read it" }).click();
  const pages = page.frameLocator('iframe[title="Soundings"]');
  await expect(pages.getByRole("heading", { name: "Soundings" })).toBeVisible();
  await expect(pages.getByText("Hugh Carrow")).toBeVisible();
  await page.getByRole("button", { name: "Next" }).click();
  await expect(pages.getByRole("heading", { name: "Chapter 1" })).toBeVisible();
  await expect(pages.getByText("The words read from the scan of page 1.")).toBeVisible();
  // It is set in the typeface that comes with it.
  const text = pages.getByText("The words read from the scan of page 1.");
  await expect(text).toHaveCSS("font-family", /Ibarra Real Nova/);
  await expect.poll(() => text.evaluate(() => document.fonts.check('16px "Ibarra Real Nova"'))).toBe(true);
  await page.getByLabel("Contents").selectOption({ label: "The falmouth station" });
  await expect(pages.getByText("It was a quiet morning in the library")).toBeVisible();

  await page.goto(`/books/${book.id}`);
  await expect(page.getByRole("listitem").filter({ hasText: "Soundings - Hugh Carrow.epub" })).toBeVisible();
  await expect(page.getByRole("listitem").filter({ hasText: "Soundings - Hugh Carrow.pdf" })).toBeVisible();

  await page.goto("/activity");
  await expect(page.getByRole("listitem").filter({ hasText: 'Restore "Soundings"' }).first()).toContainText("Done");

  // Activity leads back to what the restore did.
  await page.getByRole("link", { name: 'Restore "Soundings"' }).first().click();
  await expect(page).toHaveURL(`/books/${book.id}/restore`);
  await expect(page.getByRole("heading", { name: "The book has been restored" })).toBeVisible();
  await expect(page.getByRole("list", { name: "Passages to check" }).getByRole("listitem")).toHaveCount(8);
});

test("ends after the first pages when the book would not come out well", async ({ page, request }) => {
  const saved = await request.put("/api/restore/key", { data: { key: "sk-ant-test" } });
  expect(saved.status()).toBe(200);
  fs.writeFileSync(modelDecline, "The pages are too faint to read.");
  const book = await addBook(request, "lead-line.pdf");

  // Without a failed conversion, the way in is a plain link among the file actions.
  await page.goto(`/books/${book.id}/edit`);
  await page.getByRole("link", { name: "Restore…" }).click();
  await page.getByRole("button", { name: "Restore the book" }).click();
  await expect(page.getByRole("alert")).toHaveText(
    "The restore was ended after the first pages: The pages are too faint to read. Nothing was added to the book.",
  );
  await expect(page.getByRole("button", { name: "Restore the book" })).toBeEnabled();

  await page.goto(`/books/${book.id}`);
  await expect(page.getByRole("listitem").filter({ hasText: ".epub" })).toHaveCount(0);
});

test("offers a restore only for books with a PDF", async ({ page, request }) => {
  const book = await addBook(request, "kindred.epub");
  await page.goto(`/books/${book.id}/edit`);
  await expect(page.getByRole("button", { name: "Convert", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Restore…" })).toHaveCount(0);
  await page.goto(`/books/${book.id}/restore`);
  await expect(page.getByRole("alert")).toHaveText("Only books with a PDF can be restored for now.");
});
