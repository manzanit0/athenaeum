import { expect, test } from "@playwright/test";
import fs from "node:fs";
import { addBook, fixture } from "./helpers";

const almanac = "The Tidewater Almanac";

test("reads an EPUB section by section", async ({ page, request }) => {
  const book = await addBook(request, "almanac.epub");
  await page.goto(`/books/${book.id}`);
  await page.getByRole("link", { name: /^Read / }).click();
  await expect(page).toHaveURL(`/books/${book.id}/preview`);

  const pages = page.frameLocator(`iframe[title="${almanac}"]`);
  const contents = page.getByLabel("Contents");
  await expect(pages.getByRole("heading", { name: "Spring Tides" })).toBeVisible();
  await expect(page.getByText("Section 1 of 3")).toBeVisible();
  await expect(page.getByRole("button", { name: "Previous" })).toBeDisabled();

  await page.getByRole("button", { name: "Next" }).click();
  await expect(pages.getByRole("heading", { name: "Neap Tides" })).toBeVisible();
  await expect(page.getByText("Section 2 of 3")).toBeVisible();
  await expect(contents.locator("option:checked")).toHaveText("Neap Tides");

  await contents.selectOption({ label: "Storm Surge" });
  await expect(pages.getByRole("heading", { name: "Storm Surge" })).toBeVisible();
  await expect(page.getByText("Section 3 of 3")).toBeVisible();
  await expect(page.getByRole("button", { name: "Next" })).toBeDisabled();

  const textSize = () => pages.locator("html").evaluate((html) => parseFloat(getComputedStyle(html).fontSize));
  const before = await textSize();
  await page.getByRole("button", { name: "Larger text" }).click();
  await expect.poll(textSize).toBeGreaterThan(before);

  await page.getByRole("button", { name: "Previous" }).click();
  await expect(pages.getByRole("heading", { name: "Neap Tides" })).toBeVisible();
  // The size is put on a section once it has loaded, which can be after
  // its heading shows.
  await expect.poll(textSize).toBeGreaterThan(before);

  await page.getByRole("link", { name: "Back to the book" }).click();
  await expect(page).toHaveURL(`/books/${book.id}`);
});

test("previews a PDF and switches between the files of a book", async ({ page, request }) => {
  const book = await addBook(request, "handbook.pdf");
  await page.goto(`/books/${book.id}/preview`);
  const viewer = page.locator('iframe[title="Typography Handbook"]');
  await expect(viewer).toHaveAttribute("src", `/api/books/${book.id}/preview/pdf/`);
  const pdf = await request.get(`/api/books/${book.id}/preview/pdf/`);
  expect(pdf.headers()["content-type"]).toBe("application/pdf");
  expect(pdf.headers()["content-disposition"]).toBe("inline");
  await expect(page.getByRole("link", { name: "Open the PDF in a new tab" })).toHaveAttribute("href", `/api/books/${book.id}/preview/pdf/`);

  const added = await request.post(`/api/books/${book.id}/formats`, {
    multipart: { files: fs.createReadStream(fixture("almanac.epub")) },
  });
  expect(added.status()).toBe(201);
  await page.reload();
  await expect(page.getByLabel("File")).toHaveValue("epub");
  await expect(page.frameLocator("iframe").getByRole("heading", { name: "Spring Tides" })).toBeVisible();

  await page.getByLabel("File").selectOption("pdf");
  await expect(viewer).toHaveAttribute("src", `/api/books/${book.id}/preview/pdf/`);
  await expect(page.getByLabel("Contents")).toHaveCount(0);
});

test("converts a Kindle book before showing it", async ({ page, request }) => {
  const book = await addBook(request, "roadside.mobi");
  await page.goto(`/books/${book.id}/preview`);
  const pages = page.frameLocator('iframe[title="Roadside Picnic"]');
  await expect(pages.getByRole("heading", { name: "Spring Tides" })).toBeVisible();
  await expect(page.getByText("Section 1 of 3")).toBeVisible();

  await page.goto("/activity");
  await expect(page.getByText("Prepare a preview of “Roadside Picnic”").or(page.getByText('Prepare a preview of "Roadside Picnic"'))).toBeVisible();
});
