import { expect, test } from "@playwright/test";
import { addBook, shelfBook } from "./helpers";

test("keeps the shelves beside the books and one row of controls above them", async ({ page, request }) => {
  await addBook(request, "middlemarch.epub");
  await page.goto("/");
  const shelves = page.getByRole("complementary", { name: "Narrow the shelf" });
  const book = shelfBook(page, "Middlemarch", "George Eliot").first();
  await expect(shelves).toBeVisible();
  await expect(book).toBeVisible();
  await expect(page.getByRole("button", { name: "Filters", exact: true })).toBeHidden();
  await expect(page.getByRole("heading", { level: 1, name: "Library" })).toHaveCount(1);
  await expect(page.getByLabel("Search library")).toHaveAttribute("placeholder", /^Search \d+ books?$/);

  const controls = [
    page.getByLabel("Search library"),
    page.getByRole("button", { name: "Sort", exact: true }),
    page.getByRole("group", { name: "Show books as" }).getByRole("button", { name: "Grid" }),
    page.getByRole("group", { name: "Show books as" }).getByRole("button", { name: "List" }),
    page.getByText("Add books"),
  ];
  const boxes = await Promise.all(controls.map((control) => control.boundingBox()));
  const middles = boxes.map((box) => box!.y + box!.height / 2);
  expect(Math.max(...middles) - Math.min(...middles)).toBeLessThan(6);
  const [panel, cover] = await Promise.all([shelves.boundingBox(), book.boundingBox()]);
  expect(cover!.x).toBeGreaterThan(panel!.x + panel!.width - 1);

  const box = page.getByLabel("Select Middlemarch").first();
  await expect(box).toHaveCSS("opacity", "0");
  await book.hover();
  await expect(box).toHaveCSS("opacity", "1");
});

test("opens the shelves between the controls and the books in a narrow window", async ({ page, request }) => {
  await addBook(request, "middlemarch.epub");
  await page.setViewportSize({ width: 390, height: 800 });
  await page.goto("/");
  const shelves = page.getByRole("complementary", { name: "Narrow the shelf" });
  const toggle = page.getByRole("button", { name: "Filters", exact: true });
  const book = shelfBook(page, "Middlemarch", "George Eliot").first();
  await expect(book).toBeVisible();
  await expect(shelves).toBeHidden();
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByRole("navigation", { name: "Sections" }).getByRole("link", { name: "Kindle" })).toBeVisible();

  await toggle.click();
  await expect(shelves).toBeVisible();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  const [above, panel, below] = await Promise.all([toggle.boundingBox(), shelves.boundingBox(), book.boundingBox()]);
  expect(panel!.y).toBeGreaterThan(above!.y);
  expect(below!.y).toBeGreaterThan(panel!.y + panel!.height - 1);

  await shelves.getByRole("button", { name: /^EPUB/ }).click();
  await toggle.click();
  await expect(shelves).toBeHidden();
  const showing = page.getByRole("group", { name: "Showing only" });
  await expect(showing).toContainText("EPUB");
  await showing.getByRole("button", { name: "Show everything" }).click();
  await expect(showing).toHaveCount(0);
});
