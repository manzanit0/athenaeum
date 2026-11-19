import { expect, test } from "@playwright/test";
import { fixture, notice, shelfBook } from "./helpers";

test("imports books in several formats and reads their details", async ({ page }) => {
  await page.goto("/");
  await page
    .getByLabel("Add books")
    .setInputFiles([fixture("left-hand.epub"), fixture("solaris.mobi"), fixture("handbook.pdf")]);

  await expect(notice(page, "Added 3 books")).toBeVisible();
  await expect(shelfBook(page, "The Left Hand of Darkness", "Ursula K. Le Guin")).toBeVisible();
  await expect(shelfBook(page, "Solaris", "Stanislaw Lem")).toBeVisible();
  await expect(shelfBook(page, "Typography Handbook", "Robert Bringhurst")).toBeVisible();

  await shelfBook(page, "The Left Hand of Darkness", "Ursula K. Le Guin").click();
  await expect(page.getByRole("heading", { level: 1, name: "The Left Hand of Darkness" })).toBeVisible();
  await expect(page.getByText("Ace Books")).toBeVisible();
  await expect(page.getByText("9780441478125")).toBeVisible();
  await expect(page.getByText(/^Hainish Cycle/)).toBeVisible();
  await expect(page.getByLabel("Publisher")).toHaveCount(0);

  await page.getByRole("link", { name: "Edit book" }).click();
  await expect(page.getByLabel("Publisher")).toHaveValue("Ace Books");
  await expect(page.getByLabel("ISBN")).toHaveValue("9780441478125");
  await expect(page.getByLabel("Series", { exact: true })).toHaveValue("Hainish Cycle");
  await expect(page.getByRole("button", { name: "Remove cover" })).toBeVisible();
});

test("rejects files that are not ebooks", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Add books").setInputFiles(fixture("malware.exe"));
  await expect(page.getByRole("alert").filter({ hasText: "malware.exe was not added" })).toBeVisible();
});

test("searches the library", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Search library").fill("lem");
  await expect(shelfBook(page, "Solaris", "Stanislaw Lem")).toBeVisible();
  await expect(shelfBook(page, "Typography Handbook", "Robert Bringhurst")).toHaveCount(0);
  await expect(page.getByText(/^1 of \d+ books$/)).toBeVisible();

  await page.getByLabel("Search library").fill("science fiction");
  await expect(shelfBook(page, "The Left Hand of Darkness", "Ursula K. Le Guin")).toBeVisible();

  await page.getByLabel("Search library").fill("no such book anywhere");
  await expect(page.getByText("No book matches")).toBeVisible();
});

test("moves between sections", async ({ page }) => {
  await page.goto("/");
  const sections = page.getByRole("navigation", { name: "Sections" });
  await sections.getByRole("link", { name: "Kindle" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Kindle" })).toBeVisible();
  await sections.getByRole("link", { name: "MEGA sync" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "MEGA sync" })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole("heading", { level: 1, name: "Kindle" })).toBeVisible();
});
